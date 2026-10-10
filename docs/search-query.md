# Search queries and filters (source builds)

Literal mode remains the default and preserves the existing tokenizer: punctuation
separates terms and terms are ANDed. Quotation marks, `OR`, `-`, and `*` have no
operator meaning in Literal mode. Advanced is opt-in.

## Advanced grammar

Advanced accepts whitespace-separated AND atoms and upper-case ASCII `OR`
branches. A branch must contain positive evidence. Prefix `-` excludes an atom
from that branch. Double quotes form a tokenizer phrase, with only `\"` and
`\\` escapes. One trailing `*` requests a token prefix (at least three letters
or numbers). Examples:

```sh
findrail search --mode advanced '"retry budget"' 
findrail search --mode advanced '"retry budget" OR backoff'
findrail search --mode advanced 'retry -deprecated'
findrail search --mode advanced 'idempot*'
findrail search --mode advanced --format pdf --path-prefix docs 'retry -deprecated'
```

PowerShell uses the same single-quoted examples. `OR` is the only Boolean
operator; use quotes to search reserved words or punctuation. Parentheses,
explicit AND/NOT/NEAR, and nested expressions are not supported. Queries are
limited to 256 Unicode code points, 32 atoms, and 8 branches. Phrase means an
FTS token phrase, not a byte substring: case and diacritic behavior follows the
bundled SQLite `unicode61 remove_diacritics 2` tokenizer. Prefix means token
prefix, not arbitrary substring. Literal title filtering is case-sensitive.
Unicode whitespace separates atoms. NUL and control characters are rejected;
CR/LF inside a quoted phrase is rejected. Lower-case `or` is a normal term.

The server filters `--format all|text|pdf|docx`, `--path-prefix` (relative,
forward-slash indexed path with segment boundary), and `--title-contains`
(literal case-sensitive substring) are ANDed with query and optional single
source scope. They are applied before total and limit. PDF/DOCX format comes
from indexed media type; filename extension is not authoritative. GitHub paths
are snapshot-relative paths.
Path filters allow at most 512 code points / 2,048 UTF-8 bytes and reject
absolute paths, backslashes, empty interior segments, `.` and `..`; one trailing
slash is normalized. Title filters allow 128 code points / 512 bytes. Neither
filter trims user-entered whitespace; `%` and `_` are literal characters.

Frozen `archive` sources use this same search engine, source scope, format/path/title
filters, preview budget, and PDF page evidence. Search and preview read only the
destination SQLite snapshot; original paths and URIs remain historical citations
and are never dereferenced.

```sh
findrail search --data-dir INDEX --path-prefix docs --title-contains runbook --json 'retry'
findrail search --data-dir INDEX --mode advanced --source SOURCE_ID --format pdf 'retry -deprecated'
```

HTTP exposes the same controls as `mode`, `format`, `path_prefix`, and
`title_contains` query parameters on `/api/v1/search`. Existing clients that omit
them remain Literal. The existing MCP `findrail_search` accepts optional fields
with the same names (query remains named `query`); its required `source_id`
continues to restrict each call to one allowlisted source.

Advanced PDF matching treats each page as a separate field for phrases, so a
phrase cannot cross a page boundary. Bare AND terms may occur on different
pages or in the title. Exclusions apply to the whole document. PDF citations
use a page with positive evidence; a title-only match has no page claim and
keeps the original URI. When several OR branches qualify, the first branch in
query order supplies page evidence. A page with all positive atoms is preferred;
positive-evidence page BM25 and then page number break ties. DOCX has no page
claims. GitHub citations retain the
full indexed commit URI. Search reads only the committed index snapshot.

Advanced search and the filters are source-build behavior and are not included
in the existing alpha.2 downloads.
