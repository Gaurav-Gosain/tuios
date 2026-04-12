//! Lua unit tests
//!
//! Runs Lua test files through ziglua to verify Lua utility functions.

const std = @import("std");
const ziglua = @import("zlua");

fn runLuaTest(lua: *ziglua.Lua, file: [:0]const u8) !void {
    lua.doFile(file) catch {
        const err_msg = lua.toString(-1) catch "(no error message)";
        std.debug.print("\nLua error: {s}\n", .{err_msg});
        return error.LuaTestFailed;
    };
}

fn setupLua(allocator: std.mem.Allocator) !*ziglua.Lua {
    var lua = try ziglua.Lua.init(allocator);
    lua.openLibs();

    // Set up package.path to find our Lua modules
    _ = try lua.getGlobal("package");
    _ = lua.pushString("src/lua/?.lua");
    lua.setField(-2, "path");
    lua.pop(1);

    return lua;
}

test "lua utils" {
    var lua = try setupLua(std.testing.allocator);
    defer lua.deinit();
    try runLuaTest(lua, "src/lua/utils_test.lua");
}

test "lua tuios" {
    var lua = try setupLua(std.testing.allocator);
    defer lua.deinit();
    try runLuaTest(lua, "src/lua/tuios_test.lua");
}

test "lua tiling" {
    var lua = try setupLua(std.testing.allocator);
    defer lua.deinit();
    try runLuaTest(lua, "src/lua/tiling_test.lua");
}

test "lua tiling zoom state" {
    var lua = try setupLua(std.testing.allocator);
    defer lua.deinit();
    try runLuaTest(lua, "src/lua/tiling_test_zoom_state.lua");
}
