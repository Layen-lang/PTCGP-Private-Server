//! Locates the installation, persists desktop preferences and starts the
//! persistent panel backend (the Go `serve-panel` process) when needed.

use crate::theme::ThemeSettings;
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

pub struct Installation {
    pub root: PathBuf,
    pub config: PathBuf,
    pub address: String,
}

impl Installation {
    /// The installation root is the nearest directory (from the executable
    /// upwards) holding `server.json`.
    pub fn locate() -> Self {
        let exe = std::env::current_exe().unwrap_or_default();
        let start = exe.parent().map(Path::to_path_buf).unwrap_or_default();
        let mut root = start.clone();
        // PTCGP_ROOT points a development build at another installation.
        let start = std::env::var_os("PTCGP_ROOT").map(PathBuf::from).unwrap_or(start);
        let mut cursor = Some(start.as_path());
        while let Some(dir) = cursor {
            if dir.join("server.json").is_file() {
                root = dir.to_path_buf();
                break;
            }
            cursor = dir.parent();
        }
        // PTCGP_CONFIG selects another configuration (side-by-side testing).
        let config = std::env::var_os("PTCGP_CONFIG").map(|c| root.join(c)).unwrap_or_else(|| root.join("server.json"));
        let address = std::fs::read_to_string(&config).ok()
            .and_then(|text| serde_json::from_str::<serde_json::Value>(&text).ok())
            .and_then(|value| value.pointer("/runtime/launcherAddress").and_then(|v| v.as_str()).map(str::to_string))
            .unwrap_or_else(|| "127.0.0.1:8080".to_string());
        Installation { root, config, address }
    }

    fn settings_path(&self) -> PathBuf { self.root.join("data").join("desktop.json") }

    pub fn load_preferences(&self) -> Preferences {
        std::fs::read_to_string(self.settings_path()).ok()
            .and_then(|text| serde_json::from_str::<Preferences>(text.trim_start_matches('\u{feff}')).ok())
            .map(|mut p| { p.theme = p.theme.sanitized(); p })
            .unwrap_or_default()
    }

    pub fn save_preferences(&self, preferences: &Preferences) {
        let path = self.settings_path();
        if let Some(parent) = path.parent() { let _ = std::fs::create_dir_all(parent); }
        if let Ok(text) = serde_json::to_string_pretty(preferences) {
            let temp = path.with_extension("json.tmp");
            if std::fs::write(&temp, text).is_ok() { let _ = std::fs::rename(temp, path); }
        }
    }

    /// The backend server: bin\ptcgp-server.exe in the installation.
    pub fn server_executable(&self) -> Option<PathBuf> {
        Some(self.root.join("bin").join("ptcgp-server.exe")).filter(|path| path.is_file())
    }

    /// Starts `serve-panel` detached so it keeps serving games after the
    /// window closes. Returns quickly; callers poll the API for readiness.
    pub fn start_panel(&self) -> anyhow::Result<()> {
        let core = self.server_executable().ok_or_else(|| anyhow::anyhow!("bin/ptcgp-server.exe introuvable dans {}", self.root.display()))?;
        let runtime = self.root.join("data").join("runtime");
        std::fs::create_dir_all(&runtime)?;
        let stdout = std::fs::File::create(runtime.join("launcher.stdout.log"))?;
        let stderr = std::fs::File::create(runtime.join("launcher.stderr.log"))?;
        let mut command = Command::new(core);
        command
            .args(["serve-panel", "--config"]).arg(&self.config)
            .arg("--project-root").arg(&self.root)
            .current_dir(&self.root)
            .env("PTCGP_DESKTOP_EXE", std::env::current_exe().unwrap_or_default())
            .stdin(Stdio::null()).stdout(stdout).stderr(stderr);
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const DETACHED_PROCESS: u32 = 0x0000_0008;
            const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            const CREATE_BREAKAWAY_FROM_JOB: u32 = 0x0100_0000;
            command.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW | CREATE_BREAKAWAY_FROM_JOB);
            if command.spawn().is_ok() { return Ok(()) }
            command.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW);
        }
        command.spawn()?;
        Ok(())
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Preferences {
    pub theme: ThemeSettings,
    pub locale: String,
}

impl Default for Preferences {
    fn default() -> Self {
        Preferences { theme: ThemeSettings::default(), locale: "en_US".into() }
    }
}
