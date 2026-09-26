# Third-party notice

PTCGP Private Server is an independent, unofficial interoperability project.
It is not affiliated with, endorsed by, sponsored by, or associated with
Nintendo, The Pokémon Company, Creatures Inc., or DeNA.

Pokémon and related names, characters, artwork, and game data are trademarks
or copyrighted material of their respective owners. They are not covered by
this repository's MIT license.

The repository and published releases contain extraction profiles and tools,
not a bundled runtime catalogue of game images or master-data tables. On first
launch, those files are imported from the user's installed game into the local
`data/generations/` directory. Locally imported game material remains the
property of its respective owners and is not covered by the project's MIT
license. Documentation screenshots may depict game imagery.

The repository does not include the game application, APK files, extracted
native libraries, production traffic captures, player accounts, or
certificates. Users are responsible for complying with the terms and laws that
apply to them.

The protocol declarations under `internal/proto` are interoperability
descriptions. Generated `.pb.go` files are produced from those declarations.
No official server source code is included or claimed.

Licenses and notices for third-party Rust dependencies used by the importer and
Android readers are collected in `native/THIRD_PARTY_NOTICES.txt` in the source
repository and `bin/THIRD_PARTY_NOTICES.txt` in the Windows release.
