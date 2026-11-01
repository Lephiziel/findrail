# ADR 0008: bounded DOCX body snapshots

Status: accepted for source builds.

Findrail reads only the main part of ordinary transitional WordprocessingML
packages, following package relationships and content types. It emits a
deterministic plain-text body snapshot, not a rendering of Word. This limited
subset avoids office-suite, subprocess, CGO and network dependencies and keeps
extraction local. Structural ambiguity/corruption fails an atomic scan; clearly
unsupported and bounded no-text outcomes are skips.

The input/ZIP/XML/text/depth/token/deadline budgets and hidden/deleted-text policy
are documented in [DOCX extraction](../docx.md). This is not a sanitization tool.
Existing folder policy migrates disabled; newly selected folders default to
8 MiB. GitHub connector policy remains text-only.

Primary references:

- [Structure of a WordprocessingML document](https://learn.microsoft.com/en-us/office/open-xml/word/structure-of-a-wordprocessingml-document)
- [Remove hidden text](https://learn.microsoft.com/en-us/office/open-xml/word/how-to-remove-hidden-text-from-a-word-processing-document)
- [WordprocessingML table](https://learn.microsoft.com/en-us/dotnet/api/documentformat.openxml.wordprocessing.table)
- [Go archive/zip](https://pkg.go.dev/archive/zip)
- [Go encoding/xml](https://pkg.go.dev/encoding/xml)
