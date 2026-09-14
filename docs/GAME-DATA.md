# Game data

Published releases include the curated runtime data needed by the server. You
should not need to download or generate it separately.

The bundle does **not** contain the game application, APK files, extracted
native libraries, production traffic captures, player accounts, certificates,
or the private acquisition workspace used during development.

## Expected layout

```text
game-data/
├── images/
│   ├── index.json
│   └── ... curated image files
└── master-data/
    ├── en_US/
    │   └── ... JSON tables
    ├── fr_FR/
    │   └── ... JSON tables
    └── ... other locales
```

No runtime file belongs directly at the root of `game-data/`.

## Master data

Locale directories under `master-data/` contain the tables consumed by the
catalog, profile editor, compatibility handlers, and Pack Studio. The server
loads a default locale with a fallback and validates relationships between
important tables at startup.

## Images

`images/index.json` is both the exact identifier-to-path mapping and the image
allowlist. A single index avoids a duplicate catalog and provides constant-time
lookup. Files that are not present in this index are not served as catalog
images.

## Compatibility

`server.json` pins the supported client, build hash, compiled protocol
fingerprint, and guarded TLS patch. Those release checks are separate from game
data. Copying newer tables into an older release does not make that release
compatible with a newer game client.

If the server reports missing or invalid data, re-extract the complete release.
Do not mix `game-data` directories from different project versions unless you
are intentionally developing and validating a new release.

## Publication boundary

Release manifests, compatibility reports, protocol descriptors, native-library
metadata, and patch-verification reports are preparation artifacts and are not
installed under `game-data/`. `images/catalog.json` is also not a runtime file;
`images/index.json` is the only installed image catalog.

The included third-party artwork and data remain the property of their
respective owners and are not covered by the source-code license. See
[NOTICE.md](../NOTICE.md).
