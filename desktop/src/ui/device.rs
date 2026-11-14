//! How an Android device is named across the panel. Emulators borrow a real
//! phone's model, so the emulator and its instance come first and the model
//! and ADB address follow as detail.

use crate::api::AndroidDevice;
use crate::i18n::t;

pub struct Label {
    pub icon: &'static str,
    pub title: String,
    pub detail: String,
}

pub fn label(d: &AndroidDevice) -> Label {
    let model = if d.model.is_empty() { d.name.as_str() } else { d.model.as_str() };
    match d.kind.as_str() {
        "emulator" => {
            let title = match (d.emulator.as_str(), d.instance.as_str()) {
                ("", _) => t("device.emulator"),
                (emulator, "") => emulator.to_string(),
                (emulator, instance) if instance.to_lowercase().contains(&emulator.to_lowercase()) => instance.to_string(),
                (emulator, instance) if instance.starts_with('#') => format!("{emulator} {instance}"),
                (emulator, instance) => format!("{emulator} · {instance}"),
            };
            Label { icon: "monitor-smartphone", title, detail: join(&[model, &d.serial]) }
        }
        kind @ ("usb" | "wifi") => {
            let via = t(if kind == "usb" { "device.usb" } else { "device.wifi" });
            // The model only adds something when the phone has a nicer name.
            let model = if model == d.name { "" } else { model };
            // Wireless debugging reaches phones by an mDNS name that says nothing.
            let address = if d.serial.contains("._adb-tls-") { "" } else { d.serial.as_str() };
            Label { icon: if kind == "usb" { "usb" } else { "wifi" }, title: name(d), detail: join(&[&via, model, address]) }
        }
        _ => Label { icon: "smartphone", title: name(d), detail: if name(d) == d.serial { String::new() } else { d.serial.clone() } },
    }
}

/// The title, followed by the ADB address when another listed device
/// shares it (two unknown emulators, two identical phones).
pub fn unique_title(d: &AndroidDevice, all: &[AndroidDevice]) -> String {
    let title = label(d).title;
    if all.iter().filter(|other| label(other).title == title).count() > 1 { format!("{title} · {}", d.serial) } else { title }
}

fn name(d: &AndroidDevice) -> String { if d.name.is_empty() { d.serial.clone() } else { d.name.clone() } }

fn join(parts: &[&str]) -> String { parts.iter().filter(|p| !p.is_empty()).copied().collect::<Vec<_>>().join(" · ") }

#[cfg(test)]
mod tests {
    use super::*;

    fn device(serial: &str, name: &str, kind: &str, emulator: &str, instance: &str) -> AndroidDevice {
        AndroidDevice { serial: serial.into(), name: name.into(), state: "device".into(), kind: kind.into(), emulator: emulator.into(), instance: instance.into(), model: "22127RK46C".into(), ..Default::default() }
    }

    #[test]
    fn emulators_lead_with_their_instance() {
        assert_eq!(label(&device("127.0.0.1:5555", "22127RK46C", "emulator", "LDPlayer", "#0")).title, "LDPlayer #0");
        assert_eq!(label(&device("127.0.0.1:16416", "x", "emulator", "MuMu", "Farm #1")).title, "MuMu · Farm #1");
        assert_eq!(label(&device("127.0.0.1:5565", "x", "emulator", "BlueStacks", "BlueStacks App Player")).title, "BlueStacks App Player");
        assert_eq!(label(&device("127.0.0.1:5555", "22127RK46C", "emulator", "LDPlayer", "#0")).detail, "22127RK46C · 127.0.0.1:5555");
    }

    #[test]
    fn phones_show_how_they_are_connected() {
        let mut phone = device("192.168.1.189:39763", "Mi 11 Lite 5G", "wifi", "", "");
        phone.model = "M2101K9G".into();
        let l = label(&phone);
        assert_eq!((l.icon, l.title.as_str()), ("wifi", "Mi 11 Lite 5G"));
        assert!(l.detail.ends_with("M2101K9G · 192.168.1.189:39763"), "{}", l.detail);
        phone.serial = "adb-a1b64c35-x7TBjW._adb-tls-connect._tcp.".into();
        assert!(label(&phone).detail.ends_with("M2101K9G"), "{}", label(&phone).detail);
    }

    #[test]
    fn identical_titles_get_their_address() {
        let a = device("emulator-5554", "x", "emulator", "", "");
        let b = device("emulator-5556", "x", "emulator", "", "");
        let all = vec![a.clone(), b];
        assert!(unique_title(&a, &all).ends_with(" · emulator-5554"));
        assert_eq!(unique_title(&a, &all[..1]), label(&a).title);
    }
}
