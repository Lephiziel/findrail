# DOCX extraction (source builds)

Findrail indexes a bounded **plain-text snapshot** from ordinary ZIP-based
`.docx` files. It reads the main WordprocessingML body only: paragraphs, runs,
tabs, line breaks, table cell text, hyperlink labels, content controls and tracked
insertions. Runs join without inferred spaces; paragraphs are separated by
newlines and table cells by ` | `. Deleted text, field instructions and direct
run-level hidden text are omitted. List labels are not numbered.

This is not a faithful view of Word and is not a sanitization/redaction tool.
Styles and inherited hidden formatting, layout, pagination, headers/footers,
comments, footnotes, drawings, embedded content, formulas, OCR and Strict OOXML
are not supported. A document without usable body text is skipped. GitHub
snapshots remain text-only.

## Bounds and outcomes

The input default is 8 MiB (configurable from 1 to 16 MiB, or `0` to disable).
Extraction also bounds ZIP entries (2,048), entry names (2,048 bytes), declared
inventory (128 MiB), each XML part (8 MiB), read XML total (16 MiB), extracted
text (1 MiB), XML depth (128), tokens (500,000), and time (5 seconds). Nothing is
written to disk or sent over a network. Text-limit / no-text / unsupported files
are skipped; malformed or ambiguous packages fail the complete scan and preserve
the prior snapshot. Successful scans prune previously indexed documents that
are now skipped.

DOCX search results and previews refer to the indexed text snapshot. Page numbers
are unavailable; copied citations use the original `file:` URI without a page
claim. The browser never reads Office XML or the original file for preview.

## Enabling

New folder indexing defaults to 8 MiB:

```sh
findrail index --max-docx-bytes 8388608 /path/to/notes
findrail start --max-docx-bytes 8388608 /path/to/notes
findrail index --max-docx-bytes 0 /path/to/text-only
```

The limit is persisted with a filesystem source. Existing sources migrate with
DOCX disabled; updating the binary does not broaden their previous policy. To
enable DOCX on a legacy folder, explicitly re-index it with `index` or
`start DIRECTORY`. Refresh without a directory uses stored limits. Schema 4
migration is transactional; stop Findrail and back up the data directory before
upgrading. Older binaries reject schema 4.

## Demo guide

Create a small ordinary Word document named `meeting-notes.docx` containing a
fictional phrase such as “Orchid planning meeting”. Add the containing folder,
search for `Orchid`, open Preview, and copy its location/citation. Save an edit
containing a new unique term and wait for watcher refresh (or use Refresh); search
for the new term. Re-index with `--max-docx-bytes 0` to disable and confirm no
DOCX match remains while text/PDF results remain. Remove the source and confirm
the original file is unchanged.

Synthetic ZIP/XML fixtures in Go tests are generated in memory; no personal
documents or office suite are needed.
