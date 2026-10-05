use ghostty_vt::{CellFlags, Rgb, Terminal};

#[test]
fn writes_text_and_reports_dirty_rows() {
    let mut t = Terminal::new(20, 4, 100).unwrap();
    t.write(b"hello\r\n\x1b[1;31mred\x1b[0m");
    let s = t.snapshot();
    assert_eq!(s.rows[0].plain().trim_end(), "hello");
    let row = &s.rows[1];
    assert!(row.cells[0].flags.contains(CellFlags::BOLD));
    let g1 = s.rows[0].generation;
    let g2 = s.rows[2].generation;
    t.write(b"\x1b[3;1Hx");
    let s = t.snapshot();
    assert_eq!(s.rows[0].generation, g1, "row 0 was not dirty");
    assert_ne!(s.rows[2].generation, g2, "row 2 changed");
    assert_eq!(s.rows[2].plain().trim_end(), "x");
}

#[test]
fn wide_and_grapheme_cells() {
    let mut t = Terminal::new(20, 2, 0).unwrap();
    t.write("\x1b[?2027h漢a👍🏽b".as_bytes());
    let s = t.snapshot();
    let r = &s.rows[0];
    assert!(r.cells[0].flags.contains(CellFlags::WIDE));
    assert!(r.cells[1].flags.contains(CellFlags::SPACER));
    assert_eq!(r.cell_text(&r.cells[0]), "漢");
    assert_eq!(r.cell_text(&r.cells[2]), "a");
    assert_eq!(r.cell_text(&r.cells[3]), "👍🏽");
}

#[test]
fn truecolor_and_inverse() {
    let mut t = Terminal::new(10, 1, 0).unwrap();
    t.write(b"\x1b[38;2;1;2;3;48;2;4;5;6mA\x1b[0m\x1b[7mB");
    let s = t.snapshot();
    let r = &s.rows[0];
    assert_eq!(r.cells[0].fg, Rgb(1, 2, 3));
    assert_eq!(r.cells[0].bg, Some(Rgb(4, 5, 6)));
    assert_eq!(r.cells[1].bg, Some(s.fg));
}

#[test]
fn keys_follow_terminal_modes() {
    use ghostty_vt::ffi::*;
    use ghostty_vt::KeyInput;
    let mut t = Terminal::new(10, 2, 0).unwrap();
    let up = KeyInput { key: GHOSTTY_KEY_ARROW_UP, ..Default::default() };
    assert_eq!(t.encode_key(&up), b"\x1b[A");
    t.write(b"\x1b[?1h");
    assert_eq!(t.encode_key(&up), b"\x1bOA");
    let a = KeyInput { key: GHOSTTY_KEY_A, text: "a", unshifted: 'a' as u32, ..Default::default() };
    assert_eq!(t.encode_key(&a), b"a");
    let ctrl_c = KeyInput { key: GHOSTTY_KEY_C, mods: GHOSTTY_MODS_CTRL as u16, text: "", unshifted: 'c' as u32, ..Default::default() };
    assert_eq!(t.encode_key(&ctrl_c), b"\x03");
    // kitty keyboard: CSI > 1 u pushes disambiguate
    t.write(b"\x1b[>1u");
    assert_eq!(t.kitty_keyboard_flags(), 1);
    let esc = KeyInput { key: GHOSTTY_KEY_ESCAPE, ..Default::default() };
    assert_eq!(t.encode_key(&esc), b"\x1b[27u");
}

#[test]
fn bracketed_paste_and_selection() {
    let mut t = Terminal::new(20, 2, 0).unwrap();
    assert_eq!(t.encode_paste("hi"), b"hi");
    t.write(b"\x1b[?2004h");
    assert_eq!(t.encode_paste("hi"), b"\x1b[200~hi\x1b[201~");
    t.write(b"hello world");
    t.select((6, 0), (10, 0), false);
    assert_eq!(t.selection_text().as_deref(), Some("world"));
    let s = t.snapshot();
    assert!(s.rows[0].cells[6].flags.contains(CellFlags::SELECTED));
    assert!(!s.rows[0].cells[5].flags.contains(CellFlags::SELECTED));
}

#[test]
fn scrollback_viewport() {
    let mut t = Terminal::new(10, 3, 1000).unwrap();
    for i in 0..10 {
        t.write(format!("line{i}\r\n").as_bytes());
    }
    assert!(t.at_bottom());
    t.scroll_delta(-2);
    assert!(!t.at_bottom());
    let s = t.snapshot();
    assert_eq!(s.rows[0].plain().trim_end(), "line6");
    t.scroll_to_bottom();
    assert!(t.at_bottom());
}
