# PDF extraction design

Planned; the current extractor only accepts UTF-8 text.

Evaluate maintained PDF text extraction libraries on a public fixture corpus
before choosing a dependency. Preserve page numbers and map each extracted
passage to its source page. Enforce input size, page count, memory, and time
limits; malformed and encrypted documents must produce visible, actionable
outcomes. Run any external helper with an explicit executable and arguments,
never a shell command composed from a filename.

Image-only PDFs should report that OCR is unavailable. OCR is a later opt-in
feature with a separate resource budget. No document upload is required for the
default extractor.
