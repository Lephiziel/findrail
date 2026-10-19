# First search with Findrail

## Add a folder and find a passage

In the writable Findrail app, choose **Add folder** and enter the absolute path of a folder on the computer running Findrail. The browser cannot browse for or upload a folder. Findrail indexes supported text, Markdown, code, text PDFs, and (when enabled) DOCX files; it does not scan your home folder or disk automatically. A read-only `serve` session does not manage sources.

When indexing finishes, enter a word you remember and choose **Search**. Select **Preview** on a result to read the indexed passage and its source. PDF results can be navigated by page; use the page controls. **Open original** and **Open containing folder** are available only when native opening is enabled and the current source supports it. They are not enabled by default. Otherwise, use **Copy location**.

## Keep useful citations

Select text in Preview and choose **Add to collection**. In **Evidence collection**, add a personal note and use **Save** or **Save changes** to persist the collection. The selected evidence, notes, and order remain available after restarting. Temporary citations that have not been saved last only in the current tab. Closing a browser tab is not the same as choosing **Quit Findrail** in the desktop app; Quit stops the app and its local server.

## Demo and troubleshooting

`findrail demo` is a separate, read-only session with synthetic documents. It does not add them to your own index. To search your own files, use the writable app and explicitly add a folder.

If no results appear, check the selected source and filters, then open **Sources** and review its current indexing status or **Indexing report**. Unsupported formats, scanned/image-only PDFs, excluded files, and size or extraction limits may explain missing text. Correct the issue in the folder and use **Refresh**; a failed scan keeps the previous committed index available.

If a status is unavailable, retry after the server is ready. A saved collection may retain historical indexed text; **Check current snapshots** compares it with the current index. Exported Markdown is a plaintext copy and cannot be recalled.
