# PDF text extraction

Implemented by `pdf.go`, composed into the filesystem adapter by the CLI.
A child of the same binary receives PDF bytes over stdin and returns page text
as bounded JSON. Input / output / page limits and a parent-enforced deadline
protect the serving process from hangs. The Go memory target is soft and is
not an OS sandbox. No network request, OCR, shell or document code execution.

Page boundaries enter document hashes, and pages are persisted atomically with
the document. Plain-text fixtures are generated from synthetic PDF objects.
See [alpha limits](../../../docs/local-alpha.md).
