#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]
//! PTCGP Private Server — native desktop panel (GPUI, as used by Zed and
//! Discord's native client). The backend is bin/ptcgp-server.exe; this
//! executable also contains the data importer and the Android readers.

mod api;
mod backend;
mod http;
mod i18n;
mod theme;
mod ui;
mod updater;

use backend::Installation;
use gpui_kit::*;
use std::sync::Arc;
use ui::app::AppView;
use ui::kit::{AppAssets, Colors};

mod readers {
    include!(concat!(env!("OUT_DIR"), "/readers.rs"));
}

/// Non-GUI roles of the executable. Returns an exit code when the process was
/// started for one of them.
fn dispatch() -> Option<i32> {
    let mut args: Vec<String> = std::env::args().collect();
    if args.len() < 2 { return None }
    // Data preparation: ptcgp-server.exe runs `<this exe> import …`.
    if args[1] == "import" {
        ptcgp_importer::set_embedded_readers(readers::READER_X86_64, readers::READER_AARCH64);
        args.remove(1);
        return Some(match ptcgp_importer::run_with(args) { Ok(()) => 0, Err(e) => { eprintln!("{e:#}"); 1 } });
    }
    // Command-line actions (status, local, online, stop…) go to the server.
    #[cfg(windows)]
    unsafe { windows_sys::Win32::System::Console::AttachConsole(windows_sys::Win32::System::Console::ATTACH_PARENT_PROCESS); }
    let inst = Installation::locate();
    let Some(server) = inst.server_executable() else { eprintln!("bin/ptcgp-server.exe introuvable"); return Some(1) };
    let mut command = std::process::Command::new(server);
    command.args(&args[1..]).current_dir(&inst.root).env("PTCGP_DESKTOP_EXE", std::env::current_exe().unwrap_or_default());
    if !args.iter().any(|a| a.eq_ignore_ascii_case("--config")) { command.arg("--config").arg(&inst.config); }
    Some(command.status().map(|s| s.code().unwrap_or(1)).unwrap_or(1))
}

/// The window title, used by the taskbar and to find the running window.
const WINDOW_TITLE: &str = "PTCGP Private Server";

/// One control panel window at a time: a second launch brings the existing
/// window to the front and exits.
#[cfg(windows)]
mod single_instance {
    use windows_sys::Win32::Foundation::{GetLastError, ERROR_ALREADY_EXISTS};
    use windows_sys::Win32::System::Threading::CreateMutexW;
    use windows_sys::Win32::UI::WindowsAndMessaging::{FindWindowW, IsIconic, SetForegroundWindow, ShowWindow, SW_RESTORE};

    fn wide(text: &str) -> Vec<u16> { text.encode_utf16().chain(Some(0)).collect() }

    /// True when this process is the only control panel. The mutex lives
    /// until the process exits.
    pub fn claim() -> bool {
        let name = wide(r"Local\ptcgp-private-server-window");
        unsafe {
            let handle = CreateMutexW(std::ptr::null(), 0, name.as_ptr());
            // Without a mutex, behave as the first instance.
            handle.is_null() || GetLastError() != ERROR_ALREADY_EXISTS
        }
    }

    pub fn focus_existing() {
        let class = wide("Zed::Window");
        let title = wide(super::WINDOW_TITLE);
        // The first instance may still be creating its window.
        for _ in 0..30 {
            unsafe {
                let window = FindWindowW(class.as_ptr(), title.as_ptr());
                if !window.is_null() {
                    if IsIconic(window) != 0 { ShowWindow(window, SW_RESTORE); }
                    SetForegroundWindow(window);
                    return;
                }
            }
            std::thread::sleep(std::time::Duration::from_millis(100));
        }
    }
}

fn main() {
    updater::startup();
    if let Some(code) = dispatch() { std::process::exit(code) }
    #[cfg(windows)]
    if !single_instance::claim() {
        single_instance::focus_existing();
        return;
    }
    let inst = Arc::new(Installation::locate());
    let prefs = inst.load_preferences();
    i18n::set_locale(&prefs.locale);

    gpui_kit::application().with_assets(AppAssets).with_http_client(http::PoolClient::new(6)).run(move |cx| {
        gpui_kit::init(cx);
        // Initial palette before the first frame so the window never flashes.
        cx.set_global(Colors(Arc::new(AppView::palette(&prefs, true))));
        let bounds = Bounds::centered(None, size(px(1440.), px(900.)), cx);
        // The app draws its own title bar in the interface colours.
        let base = gpui_kit::component::TitleBar::window_options();
        let options = WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            window_min_size: Some(size(px(860.), px(600.))),
            titlebar: Some(TitlebarOptions { title: Some(WINDOW_TITLE.into()), ..gpui_kit::component::TitleBar::title_bar_options() }),
            ..base
        };
        let inst = inst.clone();
        let prefs = prefs.clone();
        gpui_kit::open_window(options, cx, move |window, cx| cx.new(|cx| AppView::new(inst, prefs, window, cx))).expect("open window");
        cx.on_window_closed(|cx, _| if cx.windows().is_empty() { cx.quit() }).detach();
    });
}
