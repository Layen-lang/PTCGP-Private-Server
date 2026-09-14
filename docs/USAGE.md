# Using PTCGP Private Server

The control panel is the main interface. Start it with `start-server.cmd`, then
open <http://127.0.0.1:8080> if the browser does not open automatically.

## The safe daily workflow

```text
Start emulator → Open control panel → Local → Open a local profile
       ↓
Finish playing → Official (or Stop all) → Use an official account safely
```

1. Start the emulator and wait for **Emulator connected**.
2. Select **Local** and wait for **Private server active**.
3. Open an account from the **Accounts** page.
4. Make any profile or pack changes from the panel.
5. Before using an official account, select **Official**.
6. Use **Stop all** when you want to restore Android and close the local tools.

Do not launch an official account while Local mode is active.

## Connection modes

### Local

Local mode starts the private server, checks the supported client and patch
hashes, installs temporary Android routing and the local certificate, and
creates the ADB tunnel. The game is not opened until you select **Open account**.

### Official

Official mode closes the game, restores the verified native-library backup,
removes local routing and the local Android certificate, and stops the private
server. Your local profiles remain on disk and the control panel stays open.

### Stop all

Stop all performs the same Android restoration as Official mode, stops the
private server, and closes the control panel. If the emulator is unavailable,
the server stops immediately and Android restoration is recorded for the next
time that device is detected.

## Accounts

The Accounts page manages profiles that exist only in the local database.

### Create a profile

Select **New account**, then choose:

- **Empty account** for a clean inventory; or
- **God account** for cards, resources, and accessories, including the number
  of card copies per language.

Set the name, level, and experience, then create the account. You can duplicate
an existing profile when you want a safe starting point for experiments.

### Edit a profile

Choose a profile in the left column. The editor is split into:

| Section | What it changes |
| --- | --- |
| Profile | Name, level, experience, tutorial state, icon, and emblems |
| Resources | Currencies and other item quantities |
| Cards | Collection quantities by card and language |
| Battle | Playmats, sleeves, and coins |
| Showcases | Binders and display boards |

Profile identity changes require **Save profile**. Inventory changes are
applied from their own controls.

### Open a profile in the game

Local mode must be active and the emulator must be connected. Select the
profile, then **Open account**. The launcher writes the selected local account
state and starts the game.

Deleting a local profile is permanent. It does not delete or change an official
account.

## Pack Studio

Pack Studio controls the result of future local pack openings. Its active rule
is shared by all local profiles.

- **Official odds** keeps the normal pack behavior.
- **Force one table** uses a chosen compatible draw table.
- **Force one rarity** guarantees a chosen rarity where available.
- **Five cards** defines an exact composition for a selected pack.
- **Nonstandard pack** can change pack count, cards per pack, duplicate rules,
  catalog filters, free openings, and the random seed.

Review the summary before selecting **Activate this rule**. A fixed seed is
useful for reproducing the same random draw. Remove or replace experimental
rules when you want normal behavior again.

## Traffic

The Traffic page shows recent sanitized HTTP and gRPC exchanges while the local
server is running. You can pause the live view, filter by protocol or status,
and inspect decoded request and response previews.

Sanitization reduces accidental exposure but is not a guarantee that every
value is safe to publish. Review exported or copied traffic before sharing it.

## Logs

The Logs page follows launcher and mode-change events. Filter by operation,
show only warnings and errors, or export a text copy for your own diagnosis.
Technical process output is available in the expandable section at the bottom.

Never publish logs without reviewing device identifiers, local paths, and
account-related values.

## Settings

Settings controls the interface language, light or dark appearance, theme
colors, and interface size. These preferences are stored by the browser and do
not change the game or local profiles.

## Back up local profiles

1. Select **Official**, then **Stop all**.
2. Confirm that no launcher or server process is running.
3. Copy the entire `data/` directory to a private backup location.

Keep `data/` private. Do not back up or share `certs/` unless you understand the
security implications; certificates are regenerated automatically for a fresh
installation.

## Command-line shortcuts

```bat
start-server.cmd             rem Open the control panel
start-server.cmd local       rem Enable Local mode and open the panel
start-server.cmd online      rem Restore Official mode and open the panel
start-server.cmd stop        rem Restore Android and stop everything
start-server.cmd status      rem Print the current state
```

For automation-friendly JSON and device-selection options, see the
[configuration reference](CONFIGURATION.md#command-line-reference).
