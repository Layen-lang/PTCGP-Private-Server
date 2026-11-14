//! Program updates, handled by Velopack: releases are published on GitHub,
//! downloaded in the background and installed by Velopack's Update.exe, which
//! replaces the whole program folder and restarts the application.
//!
//! Only an installed application updates itself. A development build is not
//! a Velopack installation, so it never shows an update.

use std::path::Path;
use std::sync::{mpsc, Arc, Mutex};
use std::time::Duration;
use velopack::{sources, UpdateCheck, UpdateManager, VelopackApp};

const REPOSITORY: &str = "https://github.com/Layen-lang/PTCGP-Private-Server";
const CHECK_INTERVAL: Duration = Duration::from_secs(6 * 60 * 60);

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub enum Phase {
    /// Up to date, or not an installed application.
    #[default]
    Idle,
    Downloading,
    /// Downloaded and verified; installed when the user asks.
    Ready,
    /// The release list could not be read (usually no network).
    Offline,
    Error,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub struct UpdateState {
    pub phase: Phase,
    /// The release being downloaded or ready, as shown to the user.
    pub version: String,
    /// Download progress, 0 to 100.
    pub progress: f32,
    pub message: String,
}

/// Handles Velopack's install, update and uninstall invocations. Call first
/// in `main`: for those invocations it exits the process.
pub fn startup() {
    // A downloaded update is installed only on request: the backend must
    // restore the official game and stop before its files are replaced.
    VelopackApp::build().set_auto_apply_on_startup(false).run();
}

/// PTCGP_UPDATE_SOURCE (a folder or a URL holding a release feed) replaces
/// GitHub, to test an update before publishing it.
fn manager() -> Result<UpdateManager, velopack::Error> {
    match std::env::var("PTCGP_UPDATE_SOURCE") {
        Ok(place) if Path::new(&place).is_dir() => UpdateManager::new(sources::FileSource::new(place), None, None),
        Ok(place) if !place.is_empty() => UpdateManager::new(sources::HttpSource::new(place), None, None),
        _ => UpdateManager::new(sources::GithubSource::new(REPOSITORY, None, false), None, None),
    }
}

/// Package versions have three numbers; the project shows four
/// (game version, then revision): 1.8.103 is release 1.8.1.3.
pub fn display_version(package: &str) -> String {
    let parts: Vec<u64> = package.split(['.', '-', '+']).take(3).filter_map(|p| p.parse().ok()).collect();
    match parts[..] {
        [major, minor, patch] => format!("{major}.{minor}.{}.{}", patch / 100, patch % 100),
        _ => package.to_string(),
    }
}

#[derive(Clone, Default)]
pub struct Updater(Arc<Mutex<UpdateState>>);

impl Updater {
    /// Checks now, then every six hours, downloading what it finds.
    pub fn start() -> Self {
        let updater = Updater::default();
        let shared = updater.clone();
        std::thread::spawn(move || {
            let Ok(manager) = manager() else { return };
            loop {
                shared.check(&manager);
                std::thread::sleep(CHECK_INTERVAL);
            }
        });
        updater
    }

    pub fn state(&self) -> UpdateState { self.0.lock().map(|s| s.clone()).unwrap_or_default() }

    fn set(&self, phase: Phase, version: &str, message: String) {
        if let Ok(mut state) = self.0.lock() {
            *state = UpdateState { phase, version: display_version(version), progress: if phase == Phase::Ready { 100.0 } else { 0.0 }, message };
        }
    }

    fn check(&self, manager: &UpdateManager) {
        let pending = manager.get_update_pending_restart();
        let ready = |version: &str| self.set(Phase::Ready, version, String::new());
        let update = match manager.check_for_updates() {
            Ok(UpdateCheck::UpdateAvailable(update)) => update,
            Ok(_) => return match pending { Some(asset) => ready(&asset.Version), None => self.set(Phase::Idle, "", String::new()) },
            // An update downloaded earlier stays installable offline.
            Err(error) => return match pending { Some(asset) => ready(&asset.Version), None => self.set(Phase::Offline, "", error.to_string()) },
        };
        let version = update.TargetFullRelease.Version.clone();
        if pending.is_some_and(|asset| asset.Version == version) { return ready(&version) }
        self.set(Phase::Downloading, &version, String::new());
        let (sender, progress) = mpsc::channel::<i16>();
        let result = std::thread::scope(|scope| {
            scope.spawn(|| for percent in progress {
                if let Ok(mut state) = self.0.lock() { state.progress = f32::from(percent).clamp(0.0, 100.0) }
            });
            manager.download_updates(&update, Some(sender))
        });
        match result {
            Ok(()) => ready(&version),
            Err(error) => self.set(Phase::Error, &version, error.to_string()),
        }
    }
}

/// Installs the downloaded update and restarts. The backend must have
/// exited. Returns only on failure.
pub fn apply() -> anyhow::Result<()> {
    let manager = manager()?;
    let asset = manager.get_update_pending_restart().ok_or_else(|| anyhow::anyhow!("no downloaded update"))?;
    // Update.exe waits for this process to exit, replaces the program
    // folder and starts the new version.
    manager.wait_exit_then_apply_updates(&asset, false, true, Vec::<String>::new())?;
    std::process::exit(0)
}

/// Waits for a process (the backend) to exit. True when it is gone.
#[cfg(windows)]
pub fn wait_for_exit(pid: u32, timeout: Duration) -> bool {
    use windows_sys::Win32::Foundation::{CloseHandle, WAIT_OBJECT_0};
    use windows_sys::Win32::System::Threading::{OpenProcess, WaitForSingleObject, PROCESS_SYNCHRONIZE};
    unsafe {
        let handle = OpenProcess(PROCESS_SYNCHRONIZE, 0, pid);
        // No such process: it has already exited.
        if handle.is_null() { return true }
        let result = WaitForSingleObject(handle, timeout.as_millis() as u32);
        CloseHandle(handle);
        result == WAIT_OBJECT_0
    }
}

#[cfg(not(windows))]
pub fn wait_for_exit(_pid: u32, timeout: Duration) -> bool { std::thread::sleep(timeout.min(Duration::from_secs(2))); true }

#[cfg(test)]
mod tests {
    #[test]
    fn package_versions_are_shown_with_four_numbers() {
        assert_eq!(super::display_version("1.8.100"), "1.8.1.0");
        assert_eq!(super::display_version("1.8.103"), "1.8.1.3");
        assert_eq!(super::display_version("1.10.1203"), "1.10.12.3");
        assert_eq!(super::display_version("nightly"), "nightly");
    }
}
