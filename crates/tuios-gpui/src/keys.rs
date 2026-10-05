//! Turns GPUI key events into ghostty key events. ghostty's encoder then
//! produces the bytes, honouring the modes the program in the pane set
//! (cursor keys, keypad, modifyOtherKeys and the kitty keyboard protocol).

use ghostty_vt::ffi::*;
use ghostty_vt::{KeyAction, KeyInput};
use gpui::Keystroke;

/// The ghostty key for a GPUI key name (the US-layout key on the keyboard).
pub fn key_code(key: &str) -> GhosttyKey {
    let k = key;
    if k.len() == 1 {
        let c = k.as_bytes()[0].to_ascii_lowercase();
        return match c {
            b'a'..=b'z' => GHOSTTY_KEY_A + (c - b'a') as GhosttyKey,
            b'0'..=b'9' => GHOSTTY_KEY_DIGIT_0 + (c - b'0') as GhosttyKey,
            b'`' => GHOSTTY_KEY_BACKQUOTE,
            b'\\' => GHOSTTY_KEY_BACKSLASH,
            b'[' => GHOSTTY_KEY_BRACKET_LEFT,
            b']' => GHOSTTY_KEY_BRACKET_RIGHT,
            b',' => GHOSTTY_KEY_COMMA,
            b'=' => GHOSTTY_KEY_EQUAL,
            b'-' => GHOSTTY_KEY_MINUS,
            b'.' => GHOSTTY_KEY_PERIOD,
            b'\'' => GHOSTTY_KEY_QUOTE,
            b';' => GHOSTTY_KEY_SEMICOLON,
            b'/' => GHOSTTY_KEY_SLASH,
            b' ' => GHOSTTY_KEY_SPACE,
            _ => GHOSTTY_KEY_UNIDENTIFIED,
        };
    }
    match k {
        "enter" => GHOSTTY_KEY_ENTER,
        "tab" => GHOSTTY_KEY_TAB,
        "space" => GHOSTTY_KEY_SPACE,
        "backspace" => GHOSTTY_KEY_BACKSPACE,
        "escape" => GHOSTTY_KEY_ESCAPE,
        "delete" => GHOSTTY_KEY_DELETE,
        "insert" => GHOSTTY_KEY_INSERT,
        "home" => GHOSTTY_KEY_HOME,
        "end" => GHOSTTY_KEY_END,
        "pageup" => GHOSTTY_KEY_PAGE_UP,
        "pagedown" => GHOSTTY_KEY_PAGE_DOWN,
        "up" => GHOSTTY_KEY_ARROW_UP,
        "down" => GHOSTTY_KEY_ARROW_DOWN,
        "left" => GHOSTTY_KEY_ARROW_LEFT,
        "right" => GHOSTTY_KEY_ARROW_RIGHT,
        "f1" => GHOSTTY_KEY_F1,
        "f2" => GHOSTTY_KEY_F2,
        "f3" => GHOSTTY_KEY_F3,
        "f4" => GHOSTTY_KEY_F4,
        "f5" => GHOSTTY_KEY_F5,
        "f6" => GHOSTTY_KEY_F6,
        "f7" => GHOSTTY_KEY_F7,
        "f8" => GHOSTTY_KEY_F8,
        "f9" => GHOSTTY_KEY_F9,
        "f10" => GHOSTTY_KEY_F10,
        "f11" => GHOSTTY_KEY_F11,
        "f12" => GHOSTTY_KEY_F12,
        "shift" => GHOSTTY_KEY_SHIFT_LEFT,
        "control" => GHOSTTY_KEY_CONTROL_LEFT,
        "alt" => GHOSTTY_KEY_ALT_LEFT,
        "platform" => GHOSTTY_KEY_META_LEFT,
        _ => GHOSTTY_KEY_UNIDENTIFIED,
    }
}

pub fn mods(k: &Keystroke) -> u16 {
    let m = &k.modifiers;
    let mut v = 0u32;
    if m.shift {
        v |= GHOSTTY_MODS_SHIFT;
    }
    if m.control {
        v |= GHOSTTY_MODS_CTRL;
    }
    if m.alt {
        v |= GHOSTTY_MODS_ALT;
    }
    if m.platform {
        v |= GHOSTTY_MODS_SUPER;
    }
    v as u16
}

/// Whether the keystroke is plain text input that an input method may want:
/// it produces characters and carries no ctrl, alt or super.
pub fn is_text(k: &Keystroke) -> bool {
    let m = &k.modifiers;
    !m.control && !m.alt && !m.platform && k.key_char.as_deref().is_some_and(|c| !c.is_empty() && !c.chars().any(char::is_control))
}

/// Builds the ghostty event for a keystroke. `text` must outlive the result.
pub fn key_input<'a>(k: &Keystroke, text: &'a str, action: KeyAction) -> KeyInput<'a> {
    let key = key_code(&k.key);
    let unshifted = if k.key.chars().count() == 1 { k.key.chars().next().map(|c| c.to_ascii_lowercase() as u32).unwrap_or(0) } else if k.key == "space" { ' ' as u32 } else { 0 };
    let consumed = if k.modifiers.shift && !text.is_empty() { GHOSTTY_MODS_SHIFT as u16 } else { 0 };
    KeyInput { key, mods: mods(k), consumed_mods: consumed, text, unshifted, action }
}

/// The text a keystroke types, for the encoder: the produced character when
/// there is one and no ctrl or super is held. Ctrl combinations are encoded
/// from the key itself.
pub fn key_text(k: &Keystroke) -> String {
    if k.modifiers.control || k.modifiers.platform {
        return String::new();
    }
    match k.key_char.as_deref() {
        Some(c) if !c.chars().any(char::is_control) => c.to_string(),
        _ => String::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ghostty_vt::Terminal;

    fn ks(s: &str) -> Keystroke {
        let mut k = Keystroke::parse(s).unwrap();
        if k.key_char.is_none() && k.key.len() == 1 && !k.modifiers.control && !k.modifiers.platform {
            let c = if k.modifiers.shift { k.key.to_uppercase() } else { k.key.clone() };
            k.key_char = Some(c);
        }
        k
    }

    fn enc(t: &mut Terminal, s: &str) -> Vec<u8> {
        let k = ks(s);
        let text = key_text(&k);
        t.encode_key(&key_input(&k, &text, KeyAction::Press))
    }

    #[test]
    fn legacy_encoding() {
        let mut t = Terminal::new(10, 2, 0).unwrap();
        assert_eq!(enc(&mut t, "a"), b"a");
        assert_eq!(enc(&mut t, "shift-a"), b"A");
        assert_eq!(enc(&mut t, "ctrl-c"), b"\x03");
        assert_eq!(enc(&mut t, "enter"), b"\r");
        assert_eq!(enc(&mut t, "backspace"), b"\x7f");
        assert_eq!(enc(&mut t, "alt-b"), b"\x1bb");
        assert_eq!(enc(&mut t, "ctrl-left"), b"\x1b[1;5D");
        assert_eq!(enc(&mut t, "f5"), b"\x1b[15~");
    }

    #[test]
    fn kitty_protocol_when_the_app_asks() {
        let mut t = Terminal::new(10, 2, 0).unwrap();
        t.write(b"\x1b[>1u");
        assert_eq!(enc(&mut t, "escape"), b"\x1b[27u");
        assert_eq!(enc(&mut t, "ctrl-i"), b"\x1b[105;5u");
        t.write(b"\x1b[<u");
        assert_eq!(enc(&mut t, "escape"), b"\x1b");
    }

    #[test]
    fn text_detection() {
        assert!(is_text(&ks("a")));
        assert!(!is_text(&ks("ctrl-a")));
        assert!(!is_text(&ks("enter")));
    }
}
