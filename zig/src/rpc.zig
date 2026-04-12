//! MessagePack-RPC protocol encoding and decoding.

const std = @import("std");

const msgpack = @import("msgpack.zig");

const Allocator = std.mem.Allocator;

const log = std.log.scoped(.rpc);

/// msgpack-RPC message types
pub const MessageType = enum(u8) {
    request = 0,
    response = 1,
    notification = 2,
};

/// Request: [type=0, msgid, method, params]
pub const Request = struct {
    msgid: u32,
    method: []const u8,
    params: msgpack.Value,

    pub fn deinit(self: Request, allocator: Allocator) void {
        allocator.free(self.method);
        self.params.deinit(allocator);
    }
};

/// Response: [type=1, msgid, error, result]
pub const Response = struct {
    msgid: u32,
    err: ?msgpack.Value,
    result: msgpack.Value,

    pub fn deinit(self: Response, allocator: Allocator) void {
        if (self.err) |e| e.deinit(allocator);
        self.result.deinit(allocator);
    }
};

/// Notification: [type=2, method, params]
pub const Notification = struct {
    method: []const u8,
    params: msgpack.Value,

    pub fn deinit(self: Notification, allocator: Allocator) void {
        allocator.free(self.method);
        self.params.deinit(allocator);
    }
};

pub const Message = union(MessageType) {
    request: Request,
    response: Response,
    notification: Notification,

    pub fn deinit(self: Message, allocator: Allocator) void {
        switch (self) {
            .request => |r| r.deinit(allocator),
            .response => |r| r.deinit(allocator),
            .notification => |n| n.deinit(allocator),
        }
    }
};

pub const DecodeError = error{
    InvalidMessageFormat,
    InvalidMessageType,
    InvalidArrayLength,
    NotAnArray,
    NotAnInteger,
    NotAString,
} || msgpack.DecodeError;

pub const DecodeResult = struct {
    message: Message,
    bytes_consumed: usize,
};

pub fn decodeMessage(allocator: Allocator, data: []const u8) DecodeError!Message {
    std.debug.assert(data.len > 0);

    var decoder = msgpack.Decoder.init(allocator, data);

    const len = try decoder.readArrayLen();
    if (len < 3) return error.InvalidArrayLength;
    std.debug.assert(len >= 3 and len <= 4);

    const msg_type = try decoder.readInt();
    std.debug.assert(msg_type >= 0 and msg_type <= 2);

    switch (msg_type) {
        0 => { // Request: [0, msgid, method, params]
            if (len != 4) return error.InvalidArrayLength;
            const msgid = @as(u32, @intCast(try decoder.readInt()));
            const method = try decoder.readString();
            errdefer allocator.free(method);
            const params = try decoder.decode();

            return .{
                .request = .{
                    .msgid = msgid,
                    .method = method,
                    .params = params,
                },
            };
        },
        1 => { // Response: [1, msgid, error, result]
            if (len != 4) return error.InvalidArrayLength;
            const msgid = @as(u32, @intCast(try decoder.readInt()));

            var err_val: ?msgpack.Value = null;
            const byte = try decoder.peekByte();
            if (byte == 0xc0) {
                _ = try decoder.readByte(); // consume nil
            } else {
                err_val = try decoder.decode();
            }
            errdefer if (err_val) |e| e.deinit(allocator);

            const result = try decoder.decode();

            return .{
                .response = .{
                    .msgid = msgid,
                    .err = err_val,
                    .result = result,
                },
            };
        },
        2 => { // Notification: [2, method, params]
            if (len != 3) return error.InvalidArrayLength;
            const method = try decoder.readString();
            errdefer allocator.free(method);
            const params = try decoder.decode();

            return .{
                .notification = .{
                    .method = method,
                    .params = params,
                },
            };
        },
        else => return error.InvalidMessageType,
    }
}

pub fn decodeMessageWithSize(allocator: Allocator, data: []const u8) DecodeError!DecodeResult {
    std.debug.assert(data.len > 0);

    var decoder = msgpack.Decoder.init(allocator, data);

    const len = try decoder.readArrayLen();
    if (len < 3) return error.InvalidArrayLength;
    std.debug.assert(len >= 3 and len <= 4);

    const msg_type = try decoder.readInt();
    std.debug.assert(msg_type >= 0 and msg_type <= 2);

    const message: Message = switch (msg_type) {
        0 => blk: { // Request: [0, msgid, method, params]
            if (len != 4) return error.InvalidArrayLength;
            const msgid = @as(u32, @intCast(try decoder.readInt()));
            const method = try decoder.readString();
            errdefer allocator.free(method);
            const params = try decoder.decode();

            break :blk .{
                .request = .{
                    .msgid = msgid,
                    .method = method,
                    .params = params,
                },
            };
        },
        1 => blk: { // Response: [1, msgid, error, result]
            if (len != 4) return error.InvalidArrayLength;
            const msgid = @as(u32, @intCast(try decoder.readInt()));

            var err_val: ?msgpack.Value = null;
            const byte = try decoder.peekByte();
            if (byte == 0xc0) {
                _ = try decoder.readByte(); // consume nil
            } else {
                err_val = try decoder.decode();
            }
            errdefer if (err_val) |e| e.deinit(allocator);

            const result = try decoder.decode();

            break :blk .{
                .response = .{
                    .msgid = msgid,
                    .err = err_val,
                    .result = result,
                },
            };
        },
        2 => blk: { // Notification: [2, method, params]
            if (len != 3) return error.InvalidArrayLength;
            const method = try decoder.readString();
            errdefer allocator.free(method);
            const params = try decoder.decode();

            break :blk .{
                .notification = .{
                    .method = method,
                    .params = params,
                },
            };
        },
        else => return error.InvalidMessageType,
    };

    return .{
        .message = message,
        .bytes_consumed = decoder.pos,
    };
}

const testing = std.testing;

test "decode request" {
    // [0, 1, "test_method", []]
    const data = try msgpack.encode(testing.allocator, .{ 0, 1, "test_method", .{} });
    defer testing.allocator.free(data);

    const msg = try decodeMessage(testing.allocator, data);
    defer msg.deinit(testing.allocator);

    try testing.expect(msg == .request);
    try testing.expectEqual(@as(u32, 1), msg.request.msgid);
    try testing.expectEqualStrings("test_method", msg.request.method);
}

test "decode response success" {
    // [1, 1, nil, 42]
    const data = try msgpack.encode(testing.allocator, .{ 1, 1, null, 42 });
    defer testing.allocator.free(data);

    const msg = try decodeMessage(testing.allocator, data);
    defer msg.deinit(testing.allocator);

    try testing.expect(msg == .response);
    try testing.expectEqual(@as(u32, 1), msg.response.msgid);
    try testing.expect(msg.response.err == null);
    try testing.expect(msg.response.result.unsigned == 42);
}

test "decode notification" {
    // [2, "event_name", {}]
    const data = try msgpack.encode(testing.allocator, .{ 2, "event_name", .{} });
    defer testing.allocator.free(data);

    const msg = try decodeMessage(testing.allocator, data);
    defer msg.deinit(testing.allocator);

    try testing.expect(msg == .notification);
    try testing.expectEqualStrings("event_name", msg.notification.method);
}
