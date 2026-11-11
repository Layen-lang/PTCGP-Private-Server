//! Embeds the application icon and version details, and compiles the two
//! Android readers into the application. They are pushed to
//! emulators from memory during data preparation and never written on the
//! computer. Build them with native/build.ps1; PTCGP_READERS_DIR overrides
//! their folder (default: native/target/readers).

use std::path::PathBuf;

fn main() {
    println!("cargo:rerun-if-env-changed=PTCGP_READERS_DIR");
    let dir = std::env::var_os("PTCGP_READERS_DIR").map(PathBuf::from).unwrap_or_else(|| PathBuf::from("../native/target/readers"));
    let out = PathBuf::from(std::env::var("OUT_DIR").unwrap());
    let mut code = String::new();
    for (constant, file) in [("READER_X86_64", "ptcgp-reader-x86_64"), ("READER_AARCH64", "ptcgp-reader-aarch64")] {
        let source = dir.join(file);
        println!("cargo:rerun-if-changed={}", source.display());
        let target = out.join(file);
        // Missing readers (interface-only builds) embed an empty placeholder.
        let bytes = std::fs::read(&source).unwrap_or_default();
        if bytes.is_empty() { println!("cargo:warning={} not found: data preparation will be unavailable", source.display()) }
        std::fs::write(&target, bytes).unwrap();
        code.push_str(&format!("pub static {constant}: &[u8] = include_bytes!({:?});\n", target.display().to_string()));
    }
    std::fs::write(out.join("readers.rs"), code).unwrap();

    // Icon resource 1 is the file icon and, through GPUI, the window and
    // taskbar icon.
    println!("cargo:rerun-if-changed=assets/app.ico");
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows") {
        let version = std::fs::read_to_string("../VERSION").unwrap_or_default();
        println!("cargo:rerun-if-changed=../VERSION");
        let mut resource = winresource::WindowsResource::new();
        resource
            .set_icon("assets/app.ico")
            .set("ProductName", "PTCGP Private Server")
            .set("FileDescription", "PTCGP Private Server")
            .set("InternalName", "ptcgp-private-server")
            .set("OriginalFilename", "ptcgp-private-server.exe")
            .set("ProductVersion", version.trim())
            .set("FileVersion", version.trim());
        resource.compile().expect("compile Windows resources");
    }
}
