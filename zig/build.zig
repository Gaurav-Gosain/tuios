const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const version = getVersion(b);

    const exe_mod = b.createModule(.{
        .root_source_file = b.path("src/main.zig"),
        .target = target,
        .optimize = optimize,
    });

    const options = b.addOptions();
    options.addOption([]const u8, "version", version);
    options.addOption([]const u8, "install_prefix", b.install_prefix);
    exe_mod.addOptions("build_options", options);

    const ghostty = b.dependency("ghostty", .{
        .target = target,
        .optimize = optimize,
        .@"emit-lib-vt" = true,
    });
    exe_mod.addImport("ghostty-vt", ghostty.module("ghostty-vt"));

    const vaxis = b.dependency("vaxis", .{
        .target = target,
        .optimize = optimize,
    });
    exe_mod.addImport("vaxis", vaxis.module("vaxis"));

    const zlua = b.dependency("zlua", .{
        .target = target,
        .optimize = optimize,
        .lang = .lua54,
    });
    exe_mod.addImport("zlua", zlua.module("zlua"));

    const zeit = b.dependency("zeit", .{});
    exe_mod.addImport("zeit", zeit.module("zeit"));

    const exe = b.addExecutable(.{
        .name = "tuios",
        .root_module = exe_mod,
    });

    b.installArtifact(exe);

    // Lua files
    b.installFile("src/lua/prise.lua", "share/tuios/lua/prise.lua");
    b.installFile("src/lua/tiling.lua", "share/tuios/lua/prise_tiling_ui.lua");
    b.installFile("src/lua/utils.lua", "share/tuios/lua/utils.lua");

    // Run
    const run_cmd = b.addRunArtifact(exe);
    run_cmd.step.dependOn(b.getInstallStep());
    if (b.args) |args| run_cmd.addArgs(args);
    const run_step = b.step("run", "Run tuios");
    run_step.dependOn(&run_cmd.step);

    // Tests
    const tests = b.addTest(.{ .root_module = exe_mod });
    const test_step = b.step("test", "Run tests");
    test_step.dependOn(&b.addRunArtifact(tests).step);
}

fn getVersion(b: *std.Build) []const u8 {
    var code: u8 = undefined;
    const git_describe = b.runAllowFail(&.{ "git", "describe", "--match", "v*.*.*", "--tags" }, &code, .Ignore) catch {
        return "0.1.0-dev";
    };
    return std.mem.trim(u8, git_describe, " \n\r");
}
