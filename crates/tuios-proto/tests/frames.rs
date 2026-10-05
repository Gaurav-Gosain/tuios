use tuios_proto::frame::{encode_raw, FrameReader, KIND_JSON, KIND_OUTPUT, KIND_RESIZE, KIND_SNAP};
use tuios_proto::{encode_command, encode_input, Command, Message};

fn id_payload(id: &str, rest: &[u8]) -> Vec<u8> {
    let mut p = vec![id.len() as u8];
    p.extend_from_slice(id.as_bytes());
    p.extend_from_slice(rest);
    p
}

#[test]
fn decodes_every_kind_in_order() {
    let mut stream = Vec::new();
    stream.extend(encode_raw(KIND_JSON, br#"{"type":"state","state":{"session":"s","cols":80,"rows":24,"workspace":2,"num_workspaces":9,"workspace_names":{"2":"build"},"occupied":[1,2],"focused":"w1","tiling":true,"windows":[{"id":"w1","pty":"p1","title":"sh","workspace":2,"x":0,"y":0,"w":80,"h":24,"z":0,"border":0,"agent":"working"}]}}"#));
    stream.extend(encode_raw(KIND_SNAP, &id_payload("p1", &[0, 80, 0, 24, b'h', b'i'])));
    stream.extend(encode_raw(KIND_OUTPUT, &id_payload("p1", b"\x1b[1mX")));
    stream.extend(encode_raw(KIND_RESIZE, &id_payload("p1", &[0, 100, 0, 30])));
    let mut r = FrameReader::new(&stream[..]);
    let mut msgs = Vec::new();
    while let Some(f) = r.next().unwrap() {
        msgs.push(f.into_message().unwrap());
    }
    assert_eq!(msgs.len(), 4);
    match &msgs[0] {
        Message::Event(e) => {
            let s = e.state.as_ref().unwrap();
            assert_eq!(s.workspace_name(2), Some("build"));
            assert_eq!(s.visible().len(), 1);
            assert_eq!(s.windows[0].agent_state(), Some("working"));
        }
        m => panic!("{m:?}"),
    }
    assert!(matches!(&msgs[1], Message::Snapshot { pty, cols: 80, rows: 24, bytes } if pty == "p1" && bytes == b"hi"));
    assert!(matches!(&msgs[2], Message::Output { pty, bytes } if pty == "p1" && bytes == b"\x1b[1mX"));
    assert!(matches!(&msgs[3], Message::Resized { cols: 100, rows: 30, .. }));
}

#[test]
fn rejects_truncated_and_oversized_frames() {
    let mut f = encode_raw(KIND_OUTPUT, &id_payload("p", b"abc"));
    f.truncate(f.len() - 1);
    assert!(FrameReader::new(&f[..]).next().is_err());
    let huge = [0xff, 0xff, 0xff, 0xff, 2];
    assert!(FrameReader::new(&huge[..]).next().is_err());
    // An id longer than the payload is malformed, not a panic.
    let bad = encode_raw(KIND_OUTPUT, &[9, b'a']);
    assert!(FrameReader::new(&bad[..]).next().unwrap().unwrap().into_message().is_none());
}

#[test]
fn encodes_what_the_bridge_reads() {
    let f = encode_input("pty-1", b"ls\r");
    assert_eq!(&f[..4], &(1 + 1 + 5 + 3u32).to_be_bytes());
    assert_eq!(f[4], 2);
    assert_eq!(f[5], 5);
    assert_eq!(&f[6..11], b"pty-1");
    assert_eq!(&f[11..], b"ls\r");
    let c = encode_command(&Command::tape("SplitVertical", &[]));
    let json: serde_json::Value = serde_json::from_slice(&c[5..]).unwrap();
    assert_eq!(json, serde_json::json!({"cmd":"tape","command":"SplitVertical"}));
    let c = encode_command(&Command::resize(80, 24, 9, 18));
    let json: serde_json::Value = serde_json::from_slice(&c[5..]).unwrap();
    assert_eq!(json, serde_json::json!({"cmd":"resize","cols":80,"rows":24,"cell_width":9,"cell_height":18}));
}

#[test]
fn parses_ls_json() {
    let s = tuios_proto::types::parse_sessions(br#"[{"name":"a","window_count":2,"attached":true,"current_workspace":1,"dir":"tmp","windows":[]}]"#).unwrap();
    assert_eq!(s[0].name, "a");
    assert_eq!(tuios_proto::types::parse_sessions(b"null\n").unwrap().len(), 0);
}

#[test]
fn go_nulls_read_as_empty() {
    let e: tuios_proto::Event = serde_json::from_str(r#"{"type":"state","state":{"session":"s","cols":1,"rows":1,"workspace":1,"num_workspaces":9,"workspace_names":null,"occupied":null,"focused":"","tiling":false,"windows":null}}"#).unwrap();
    let s = e.state.unwrap();
    assert!(s.windows.is_empty() && s.occupied.is_empty() && s.workspace_names.is_empty());
}

#[test]
fn theme_events_parse() {
    let e: tuios_proto::Event = serde_json::from_str(r##"{"type":"theme","theme":{"name":"dracula","names":["a","b"],"light":false,"terminal":{"fg":"#f8f8f2","bg":"#282a36","cursor":"#f8f8f2","ansi":["#21222c","","","","","","","","","","","","","","",""]},"ui":{"Surface":"#3a3943"},"ground":{"Fg":"#fffaf1"},"rail_ground":"#282a36","rail_rule":"#484851","border_focused":"#8be9fd","border_focused_terminal":"#50fa7b","border_unfocused":"#ff5555","agent":{"working":"#bd93f9"}}}"##).unwrap();
    let t = e.theme.unwrap();
    assert_eq!(tuios_proto::parse_hex(&t.terminal.bg), Some(0x282a36));
    assert_eq!(tuios_proto::parse_hex(""), None);
    assert_eq!(t.ui["Surface"], "#3a3943");
}
