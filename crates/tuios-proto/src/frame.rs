//! The bridge's frame format: a 4-byte big-endian length (of everything after
//! it), a 1-byte kind, then the payload.

use crate::types::{Command, Event};
use crate::Message;
use std::io::{self, Read};

pub const KIND_JSON: u8 = 1;
pub const KIND_OUTPUT: u8 = 2;
pub const KIND_SNAP: u8 = 3;
pub const KIND_RESIZE: u8 = 4;
pub const KIND_INPUT: u8 = 2;

/// The largest frame accepted. A snapshot with a long history is the biggest.
pub const MAX_FRAME: usize = 64 << 20;

/// A frame as read, before its payload is interpreted.
#[derive(Debug, PartialEq, Eq)]
pub struct Decoded {
    pub kind: u8,
    pub payload: Vec<u8>,
}

impl Decoded {
    /// Interprets the payload. Unknown kinds and malformed payloads give None.
    pub fn into_message(self) -> Option<Message> {
        match self.kind {
            KIND_JSON => serde_json::from_slice::<Event>(&self.payload).ok().map(Message::Event),
            KIND_OUTPUT | KIND_SNAP | KIND_RESIZE => {
                let (pty, rest) = split_id(&self.payload)?;
                match self.kind {
                    KIND_OUTPUT => Some(Message::Output { pty, bytes: rest.to_vec() }),
                    KIND_SNAP => {
                        let (cols, rows, rest) = split_size(rest)?;
                        Some(Message::Snapshot { pty, cols, rows, bytes: rest.to_vec() })
                    }
                    _ => {
                        let (cols, rows, _) = split_size(rest)?;
                        Some(Message::Resized { pty, cols, rows })
                    }
                }
            }
            _ => None,
        }
    }
}

fn split_id(b: &[u8]) -> Option<(String, &[u8])> {
    let n = *b.first()? as usize;
    if b.len() < 1 + n {
        return None;
    }
    let id = std::str::from_utf8(&b[1..1 + n]).ok()?.to_string();
    Some((id, &b[1 + n..]))
}

fn split_size(b: &[u8]) -> Option<(u16, u16, &[u8])> {
    if b.len() < 4 {
        return None;
    }
    Some((u16::from_be_bytes([b[0], b[1]]), u16::from_be_bytes([b[2], b[3]]), &b[4..]))
}

/// Reads frames from a byte stream.
pub struct FrameReader<R> {
    r: R,
}

impl<R: Read> FrameReader<R> {
    pub fn new(r: R) -> Self {
        FrameReader { r }
    }

    /// The next frame, or None at a clean end of stream.
    pub fn next(&mut self) -> io::Result<Option<Decoded>> {
        let mut hdr = [0u8; 5];
        let mut got = 0;
        while got < hdr.len() {
            let n = self.r.read(&mut hdr[got..])?;
            if n == 0 {
                return if got == 0 { Ok(None) } else { Err(io::ErrorKind::UnexpectedEof.into()) };
            }
            got += n;
        }
        let len = u32::from_be_bytes([hdr[0], hdr[1], hdr[2], hdr[3]]) as usize;
        if len == 0 || len > MAX_FRAME {
            return Err(io::Error::new(io::ErrorKind::InvalidData, format!("frame length {len}")));
        }
        let mut payload = vec![0u8; len - 1];
        self.r.read_exact(&mut payload)?;
        Ok(Some(Decoded { kind: hdr[4], payload }))
    }
}

fn frame(kind: u8, payload: &[u8]) -> Vec<u8> {
    let mut b = Vec::with_capacity(5 + payload.len());
    b.extend_from_slice(&((payload.len() + 1) as u32).to_be_bytes());
    b.push(kind);
    b.extend_from_slice(payload);
    b
}

pub fn encode_input(pty: &str, bytes: &[u8]) -> Vec<u8> {
    let id = &pty.as_bytes()[..pty.len().min(255)];
    let mut p = Vec::with_capacity(1 + id.len() + bytes.len());
    p.push(id.len() as u8);
    p.extend_from_slice(id);
    p.extend_from_slice(bytes);
    frame(KIND_INPUT, &p)
}

pub fn encode_command(cmd: &Command) -> Vec<u8> {
    frame(KIND_JSON, &serde_json::to_vec(cmd).unwrap_or_default())
}

/// Encodes a frame the way the bridge does; for tests and the replay tool.
pub fn encode_raw(kind: u8, payload: &[u8]) -> Vec<u8> {
    frame(kind, payload)
}
