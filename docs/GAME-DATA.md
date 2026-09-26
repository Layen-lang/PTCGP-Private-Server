# Game data

Releases contain extraction profiles and precompiled tools. Images and master data are imported from the user’s installed game rather than distributed with the project. See [Local preparation and updates](LOCAL-DATA-UPDATES.md).

The launcher publishes validated generations under `data/generations/`. The following paths describe the catalogue layout within a generation; they are resolved by the launcher.

## Expected layout

```text
generation/
├── images/images/
│   ├── index.json
│   └── ... curated image files
└── master/output/
    ├── en_US/
    │   └── ... JSON tables
    ├── fr_FR/
    │   └── ... JSON tables
    └── ... other locales
```

The generation root also contains its validation receipt and preparation metadata.

## Master data

Locale directories under `master/output/` contain the tables consumed by the
catalog, profile editor, compatibility handlers, and Pack Studio. The server
loads a default locale with a fallback and validates relationships between
important tables at startup.

## Images

`images/images/index.json` is both the exact identifier-to-path mapping and the image
allowlist. A single index avoids a duplicate catalog and provides constant-time
lookup. Files that are not present in this index are not served as catalog
images.

## Compatibility

`server.json` pins the supported client, build hash, compiled protocol
fingerprint, and guarded TLS patch. Those release checks are separate from game
data. Copying newer tables into an older release does not make that release
compatible with a newer game client.

If the server reports missing or invalid data, retry preparation in the panel.
Do not mix generated directories from different project versions unless you
are intentionally developing and validating a new release.

## Publication boundary

Published compatibility profiles contain extraction instructions. Local generations
contain extracted files and their validation receipts. `images/images/index.json`
is the only image catalogue consumed at runtime; generations are excluded from Git
and release packages.

Locally extracted third-party artwork and data remain the property of their
respective owners and are not covered by the source-code license. See
[NOTICE.md](../NOTICE.md).
