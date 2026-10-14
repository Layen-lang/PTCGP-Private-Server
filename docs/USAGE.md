# Using PTCGP Private Server

The control panel is the main interface. Start it with `start-server.cmd`, then
open <http://127.0.0.1:8080> if the browser does not open automatically.

You can use a rooted Android emulator or a rooted physical phone. A phone can
connect over USB or Wi-Fi ADB; follow the
[phone connection guide](INSTALLATION.md#physical-android-phone-usb-or-wi-fi)
before starting Local mode. The device selector, account exclusivity, and mode
controls work the same way for either kind of device.

## The safe daily workflow

```text
Connect rooted Android device → Open panel → Prepare data if prompted → Local
→ Open a local profile → Finish playing → Official (or Stop all)
```

1. Connect the Android device and open the control panel. On a new installation,
   wait for the game data import to finish; select the device if prompted.
2. Select **Local** and wait for **Private server active**. Local mode is
   available only after preparation succeeds and the device is connected.
3. Open an account from the **Accounts** page.
   To play on a second device, select it beside **Refresh**, enable **Local**,
   and open a different account. The first game keeps running. Accounts in use
   show their device in the account list and cannot be launched elsewhere.
4. Make any profile or pack changes from the panel.
5. Before using an official account, select **Official**.
6. Use **Stop all** when you want to restore all managed devices and close the
   local tools.

**Local** and **Official** act only on the selected device. **Stop all** stops
the servers and restores all managed devices, then closes the panel. If a
device is disconnected, its restoration is deferred until it is reconnected.
Switching the selected device never reruns the shared data import.

Do not launch an official account while Local mode is active.

After the first import, the panel can open Accounts using prepared data even
when the device is offline. Reconnect it before enabling Local mode. If
preparation stops or reports an error, use **Retry preparation** on the
preparation page; see [Preparation and updates](LOCAL-DATA-UPDATES.md) for
recovery details.

## Connection modes

### Local

Local mode checks the supported client and patch hashes on the selected
device, starts its private server, installs temporary Android routing and the
local certificate, and creates its ADB tunnel. Other devices keep their current
mode. The game is not opened until you select **Open account**.

### Official

Official mode closes the game on the selected device, restores its verified
native-library backup, removes its local routing and Android certificate, and
stops its private server. Its account becomes available on another emulator.
Other game sessions continue running. Your profiles remain on disk and the
control panel stays open.

### Stop all

Stop all closes the games and performs the same Android restoration as Official
mode on every managed device, stops their private servers, and closes the
control panel after successful shutdown. If an device is unavailable, its
server stops immediately and Android restoration is recorded for the next time
that device is detected by the launcher.

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

Local mode must be active on the selected device, which must be connected.
Select the profile, then **Open account**. The launcher writes that device's
local account state and starts its game. Opening another profile on the same
device replaces that game session without repeating data preparation.

A profile can run on only one device at a time. Accounts in use display their
device and cannot be opened elsewhere. Select **Official** on that device,
or **Stop all**, to release the account. The panel also releases it after
detecting that its game or server has stopped.

Deleting a local profile is permanent. It does not delete or change an official
account. A profile in use must be released before it can be deleted.

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

The Traffic page follows the selected device's sanitized HTTP and gRPC
exchanges while its local server is running. Saved traffic history also belongs
to that emulator. You can pause the live view, filter by protocol or status, and
inspect decoded request and response previews.

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

1. Select **Stop all** and wait for shutdown to complete.
2. Confirm that no launcher or server process is running.
3. Copy the entire `data/` directory to a private backup location.

Keep `data/` private. Do not back up or share `certs/` unless you understand the
security implications; certificates are regenerated automatically for a fresh
installation.

## Command-line shortcuts

```bat
start-server.cmd             rem Open the control panel
start-server.cmd local       rem Enable Local on the selected device and open the panel
start-server.cmd online      rem Restore Official on the selected device and open the panel
start-server.cmd stop        rem Restore all managed devices and stop everything
start-server.cmd status      rem Print the selected device's state
```

For automation-friendly JSON and device-selection options, see the
[configuration reference](CONFIGURATION.md#command-line-reference).
