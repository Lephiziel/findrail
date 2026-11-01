package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Lephiziel/findrail/internal/originals"
	"github.com/Lephiziel/findrail/internal/search"
)

// searchPageTx performs count, ranked selection, provenance and PDF attribution
// against the caller's snapshot. The caller owns tx and must commit or roll it back.
func searchPageTx(ctx context.Context, tx *sql.Tx, request search.Request, offset int) (search.PageResponse, error) {
	response := search.PageResponse{Results: []search.PageResult{}, Offset: offset, Limit: request.Limit, WindowLimit: search.PageWindowLimit}
	if err := search.ValidateRequest(request); err != nil {
		return response, err
	}
	if request.Limit < 1 || request.Limit > 100 {
		return response, search.ErrLimit
	}
	if offset < 0 || offset >= search.PageWindowLimit {
		return response, fmt.Errorf("page offset outside bounded window")
	}
	advanced := request.Mode == "advanced"
	expression, _ := search.Expression(request.Query)
	var advancedPlan search.Plan
	if advanced {
		advancedPlan, _ = search.ParseAdvanced(request.Query)
		expression = compilePositivePlan(advancedPlan)
	}
	where, args := "documents_fts MATCH ?", []any{expression}
	if advanced {
		predicate, predicateArgs := advancedPredicate(advancedPlan)
		where += " AND (" + predicate + ")"
		args = append(args, predicateArgs...)
	}
	if request.SourceID != "" {
		where += " AND d.source_id=?"
		args = append(args, request.SourceID)
	}
	if request.Format != "" && request.Format != "all" {
		switch request.Format {
		case "pdf":
			where += " AND d.media_type='application/pdf'"
		case "docx":
			where += " AND d.media_type='application/vnd.openxmlformats-officedocument.wordprocessingml.document'"
		case "text":
			where += " AND d.media_type NOT IN ('application/pdf','application/vnd.openxmlformats-officedocument.wordprocessingml.document')"
		}
	}
	if request.PathPrefix != "" {
		p := strings.TrimSuffix(request.PathPrefix, "/")
		where += " AND (d.path=? OR substr(d.path,1,length(?)+1)=?||'/')"
		args = append(args, p, p, p)
	}
	if request.TitleContains != "" {
		where += " AND instr(d.title,?)>0"
		args = append(args, request.TitleContains)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid WHERE `+where, args...).Scan(&response.Total); err != nil {
		return response, err
	}
	count := request.Limit
	if remaining := search.PageWindowLimit - offset; count > remaining {
		count = remaining
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,d.title,d.uri,d.path,d.source_id,s.name,s.kind,
	        substr(snippet(documents_fts,1,'[',']','…',32),1,1025),-bm25(documents_fts,4.0,1.0),d.media_type,d.page_count,s.registration_token,s.revision,d.content_hash
        FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid JOIN sources s ON s.id=d.source_id
        WHERE `+where+` ORDER BY bm25(documents_fts,4.0,1.0),d.id LIMIT ? OFFSET ?`, append(append(args, count), offset)...)
	if err != nil {
		return response, err
	}
	for rows.Next() {
		var item search.PageResult
		var token, hash string
		var revision int64
		if err := rows.Scan(&item.ID, &item.Title, &item.URI, &item.Path, &item.SourceID, &item.SourceName, &item.SourceKind, &item.Snippet, &item.Score, &item.MediaType, &item.PageCount, &token, &revision, &hash); err != nil {
			rows.Close()
			return response, err
		}
		item.OriginalIdentity = originals.Identity(token, revision, hash, item.SourceID, item.ID)
		response.Results = append(response.Results, item)
	}
	rowErr, closeErr := rows.Err(), rows.Close()
	if rowErr != nil {
		return response, rowErr
	}
	if closeErr != nil {
		return response, closeErr
	}
	pageExpression := strings.ReplaceAll(expression, " AND ", " OR ")
	if advanced {
		pageExpression = compilePositivePlan(advancedPlan)
	}
	for i := range response.Results {
		r := &response.Results[i]
		if r.PageCount == 0 {
			continue
		}
		queryPageExpression := pageExpression
		pageSelected := false
		if advanced {
			queryPageExpression = ""
			for _, branch := range advancedPlan {
				predicate, predicateArgs := advancedPredicate(search.Plan{branch})
				candidate := compilePositivePlan(search.Plan{branch})
				checkArgs := append([]any{candidate}, predicateArgs...)
				checkArgs = append(checkArgs, r.ID)
				var found int
				checkErr := tx.QueryRowContext(ctx, `SELECT 1 FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid WHERE documents_fts MATCH ? AND (`+predicate+`) AND d.id=? LIMIT 1`, checkArgs...).Scan(&found)
				if errors.Is(checkErr, sql.ErrNoRows) {
					continue
				}
				if checkErr != nil {
					return response, checkErr
				}
				queryPageExpression = candidate
				fullErr := tx.QueryRowContext(ctx, `SELECT p.page_number,substr(snippet(pages_fts,0,'[',']','…',32),1,1025) FROM pages_fts JOIN document_pages p ON p.rowid=pages_fts.rowid WHERE pages_fts MATCH ? AND p.document_id=? ORDER BY bm25(pages_fts),p.page_number LIMIT 1`, candidate, r.ID).Scan(&r.Page, &r.Snippet)
				if fullErr == nil {
					pageSelected = true
					break
				}
				if !errors.Is(fullErr, sql.ErrNoRows) {
					return response, fullErr
				}
				queryPageExpression = branchPositiveAlternatives(branch)
				break
			}
			if pageSelected {
				r.URI += fmt.Sprintf("#page=%d", r.Page)
			} else if queryPageExpression == "" {
				r.Page = 0
				continue
			}
		}
		if pageSelected {
			continue
		}
		pageErr := tx.QueryRowContext(ctx, `SELECT p.page_number,substr(snippet(pages_fts,0,'[',']','…',32),1,1025) FROM pages_fts JOIN document_pages p ON p.rowid=pages_fts.rowid WHERE pages_fts MATCH ? AND p.document_id=? ORDER BY bm25(pages_fts),p.page_number LIMIT 1`, queryPageExpression, r.ID).Scan(&r.Page, &r.Snippet)
		if errors.Is(pageErr, sql.ErrNoRows) && advanced {
			r.Page = 0
		}
		if pageErr != nil && !errors.Is(pageErr, sql.ErrNoRows) {
			return response, pageErr
		}
		if r.Page > 0 {
			r.URI += fmt.Sprintf("#page=%d", r.Page)
		}
	}
	for i := range response.Results {
		runes := []rune(response.Results[i].Snippet)
		if len(runes) > 1024 {
			response.Results[i].Snippet = string(runes[:1024])
			response.Results[i].SnippetTruncated = true
		}
	}
	response.Returned = len(response.Results)
	return response, nil
}
