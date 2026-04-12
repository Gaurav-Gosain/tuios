//! Lua bindings for the keybind system.
//!
//! Exposes the keybind compiler and matcher to Lua scripts.
//! Usage from Lua:
//!   local keybind = require("tuios").keybind
//!   local matcher = keybind.compile({
//!       { key_string = "<D-k>v", action = "split_horizontal" },
//!       { key_string = "<D-k>s", action = "split_vertical" },
//!   }, "<D-k>")  -- optional leader
//!
//!   -- In event handler:
//!   local result = matcher:handle_key(event.data)
//!   if result.action then
//!       commands[result.action]()
//!   elseif result.pending then
//!       -- waiting for more keys
//!   end

const std = @import("std");
const ziglua = @import("zlua");

const keybind_compiler = @import("keybind_compiler.zig");
const keybind_matcher = @import("keybind_matcher.zig");
const key_string = @import("key_string.zig");
const action_mod = @import("action.zig");

const log = std.log.scoped(.keybind);

const Compiler = keybind_compiler.Compiler;
const Trie = keybind_compiler.Trie;
const Keybind = keybind_compiler.Keybind;
const Matcher = keybind_matcher.Matcher;
const Key = key_string.Key;
const Action = action_mod.Action;

// File-scope buffer for extractKey to uppercase a shifted letter key.
// Safe: Lua event handling is single-threaded, and the returned Key is
// consumed by handleKey (via keyToId) before the next call to extractKey.
var extract_key_upper: [1:0]u8 = .{0};

const MatcherHandle = struct {
    trie: *Trie,
    matcher: Matcher,
    allocator: std.mem.Allocator,

    fn deinit(self: *MatcherHandle) void {
        self.trie.deinit();
        self.allocator.destroy(self.trie);
    }
};

pub fn registerKeybindModule(lua: *ziglua.Lua) void {
    lua.createTable(0, 2);

    lua.pushFunction(ziglua.wrap(compile));
    lua.setField(-2, "compile");

    lua.pushFunction(ziglua.wrap(parseKeyString));
    lua.setField(-2, "parse_key_string");

    registerMatcherMetatable(lua);
}

fn registerMatcherMetatable(lua: *ziglua.Lua) void {
    lua.newMetatable("PriseKeybindMatcher") catch return;

    _ = lua.pushString("__index");
    lua.pushFunction(ziglua.wrap(matcherIndex));
    lua.setTable(-3);

    _ = lua.pushString("__gc");
    lua.pushFunction(ziglua.wrap(matcherGc));
    lua.setTable(-3);

    lua.pop(1);
}

fn matcherIndex(lua: *ziglua.Lua) i32 {
    const key = lua.toString(2) catch return 0;

    if (std.mem.eql(u8, key, "handle_key")) {
        lua.pushFunction(ziglua.wrap(matcherHandleKey));
        return 1;
    }
    if (std.mem.eql(u8, key, "is_pending")) {
        lua.pushFunction(ziglua.wrap(matcherIsPending));
        return 1;
    }
    if (std.mem.eql(u8, key, "reset")) {
        lua.pushFunction(ziglua.wrap(matcherReset));
        return 1;
    }

    return 0;
}

fn matcherGc(lua: *ziglua.Lua) i32 {
    const handle = lua.toUserdata(MatcherHandle, 1) catch return 0;
    handle.deinit();
    return 0;
}

fn matcherHandleKey(lua: *ziglua.Lua) i32 {
    const handle = lua.checkUserdata(MatcherHandle, 1, "PriseKeybindMatcher");
    lua.checkType(2, .table);

    const key = extractKey(lua, 2) catch {
        lua.pushNil();
        return 1;
    };

    const result = handle.matcher.handleKey(key);

    lua.createTable(0, 3);

    switch (result) {
        .action => |a| {
            switch (a.action) {
                .lua_function => |func_ref| {
                    _ = lua.rawGetIndex(ziglua.registry_index, func_ref);
                    lua.setField(-2, "func");
                },
                else => {
                    if (a.action.toString()) |name| {
                        _ = lua.pushString(name);
                        lua.setField(-2, "action");
                    }
                },
            }

            if (a.key_string) |ks| {
                _ = lua.pushString(ks);
                lua.setField(-2, "key_string");
            }
        },
        .pending => {
            lua.pushBoolean(true);
            lua.setField(-2, "pending");
        },
        .none => {
            lua.pushBoolean(true);
            lua.setField(-2, "none");
        },
    }

    return 1;
}

fn matcherIsPending(lua: *ziglua.Lua) i32 {
    const handle = lua.checkUserdata(MatcherHandle, 1, "PriseKeybindMatcher");
    lua.pushBoolean(handle.matcher.isPending());
    return 1;
}

fn matcherReset(lua: *ziglua.Lua) i32 {
    const handle = lua.checkUserdata(MatcherHandle, 1, "PriseKeybindMatcher");
    handle.matcher.reset();
    return 0;
}

fn compile(lua: *ziglua.Lua) i32 {
    _ = lua.getField(ziglua.registry_index, "tuios_ui_ptr");
    const ui_ptr = lua.toUserdata(@import("ui.zig").UI, -1) catch {
        lua.raiseErrorStr("Failed to get UI pointer", .{});
    };
    lua.pop(1);

    const allocator = ui_ptr.allocator;

    lua.checkType(1, .table);

    var leader: ?[]const u8 = null;
    if (lua.typeOf(2) == .string) {
        leader = lua.toString(2) catch null;
    }

    var compiler = Compiler.init(allocator);
    defer compiler.deinit();

    if (leader) |l| {
        compiler.setLeader(l) catch {
            lua.raiseErrorStr("Invalid leader key string", .{});
        };
    }

    var bindings_list: std.ArrayListUnmanaged(Keybind) = .empty;
    defer bindings_list.deinit(allocator);

    // Iterate over table as map: key_string => action_name | function
    lua.pushNil();
    while (lua.next(1)) {
        // Stack: key at -2, value at -1
        if (lua.typeOf(-2) != .string) {
            lua.pop(1);
            continue;
        }

        const ks = lua.toString(-2) catch {
            lua.pop(1);
            continue;
        };

        const value_type = lua.typeOf(-1);

        if (value_type == .string) {
            const action_str = lua.toString(-1) catch {
                lua.pop(1);
                continue;
            };

            const action = Action.fromString(action_str) orelse {
                lua.pop(1);
                continue;
            };

            bindings_list.append(allocator, .{
                .key_string = ks,
                .action = action,
            }) catch {
                lua.raiseErrorStr("Out of memory", .{});
            };

            lua.pop(1);
        } else if (value_type == .function) {
            const func_ref = lua.ref(ziglua.registry_index) catch {
                lua.raiseErrorStr("Failed to create function reference", .{});
            };

            bindings_list.append(allocator, .{
                .key_string = ks,
                .action = .{ .lua_function = func_ref },
            }) catch {
                lua.raiseErrorStr("Out of memory", .{});
            };
            // ref() already popped the value
        } else {
            lua.pop(1);
            continue;
        }
    }

    const trie = allocator.create(Trie) catch {
        lua.raiseErrorStr("Out of memory", .{});
    };

    trie.* = compiler.compile(bindings_list.items) catch |err| {
        allocator.destroy(trie);
        switch (err) {
            error.ConflictingBinding => lua.raiseErrorStr("Conflicting keybinding", .{}),
            error.InvalidKeyString => lua.raiseErrorStr("Invalid key string", .{}),
            error.OutOfMemory => lua.raiseErrorStr("Out of memory", .{}),
        }
    };

    const handle = lua.newUserdata(MatcherHandle, @sizeOf(MatcherHandle));
    handle.* = .{
        .trie = trie,
        .matcher = Matcher.init(trie),
        .allocator = allocator,
    };

    _ = lua.getMetatableRegistry("PriseKeybindMatcher");
    lua.setMetatable(-2);

    return 1;
}

fn parseKeyString(lua: *ziglua.Lua) i32 {
    _ = lua.getField(ziglua.registry_index, "tuios_ui_ptr");
    const ui_ptr = lua.toUserdata(@import("ui.zig").UI, -1) catch {
        lua.raiseErrorStr("Failed to get UI pointer", .{});
    };
    lua.pop(1);

    const allocator = ui_ptr.allocator;
    const input = lua.checkString(1);

    const keys = key_string.parseKeyString(allocator, input) catch |err| {
        switch (err) {
            error.InvalidFormat => lua.raiseErrorStr("Invalid key string format", .{}),
            error.UnterminatedBracket => lua.raiseErrorStr("Unterminated bracket in key string", .{}),
            error.EmptyKey => lua.raiseErrorStr("Empty key in key string", .{}),
            error.UnknownSpecialKey => lua.raiseErrorStr("Unknown special key", .{}),
        }
    };
    defer allocator.free(keys);

    lua.createTable(@intCast(keys.len), 0);

    for (keys, 0..) |key, idx| {
        lua.createTable(0, 5);

        _ = lua.pushString(key.key);
        lua.setField(-2, "key");

        lua.pushBoolean(key.ctrl);
        lua.setField(-2, "ctrl");

        lua.pushBoolean(key.alt);
        lua.setField(-2, "alt");

        lua.pushBoolean(key.shift);
        lua.setField(-2, "shift");

        lua.pushBoolean(key.super);
        lua.setField(-2, "super");

        lua.rawSetIndex(-2, @intCast(idx + 1));
    }

    return 1;
}

fn extractKey(lua: *ziglua.Lua, index: i32) !Key {
    _ = lua.getField(index, "key");
    const key_str = lua.toString(-1) catch "";
    lua.pop(1);

    _ = lua.getField(index, "ctrl");
    const ctrl = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "alt");
    const alt = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "shift");
    var shift = lua.toBoolean(-1);
    lua.pop(1);

    _ = lua.getField(index, "super");
    const super = lua.toBoolean(-1);
    lua.pop(1);

    // Don't include shift modifier when matching on generated text.
    // Shift is consumed by character generation (e.g., Shift+Comma produces '<').
    // Only strip shift for single printable characters - keep it for special keys like <S-Enter>.
    // When the key is a lowercase letter but shift is held (e.g., Alt suppressed text
    // generation so vaxis reports the unshifted codepoint), uppercase it to reflect
    // what shift would have produced.
    var effective_key = key_str;
    if (shift and key_str.len == 1 and key_str[0] >= 0x20 and key_str[0] < 0x7F) {
        if (key_str[0] >= 'a' and key_str[0] <= 'z') {
            extract_key_upper[0] = key_str[0] - 0x20;
            effective_key = extract_key_upper[0..1];
        }
        shift = false;
    }

    return Key{
        .key = effective_key,
        .ctrl = ctrl,
        .alt = alt,
        .shift = shift,
        .super = super,
    };
}
