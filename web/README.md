# Product clients

The working web interface is embedded from
`internal/transport/http/web/index.html`. It requires no Node.js build step,
external assets, account, or runtime CDN.

This directory will hold a richer client only when previews, source onboarding,
and accessibility needs justify a separate frontend build. Keep rendering of
indexed text safe, maintain keyboard / screen-reader navigation, and use the
versioned HTTP contract in `api/openapi.yaml`.

A desktop launcher is planned after local search and synchronization are useful.
It may provide an OS-approved original-file opener that the browser UI cannot.
