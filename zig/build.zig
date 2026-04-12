const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const version = getVersion(b);

    // -- Server (daemon) executable --
    const server_mod = b.createModule(.{
        .root_source_file = b.path("src/server_main.zig"),
        .target = target,
        .optimize = optimize,
    });

    const options = b.addOptions();
    options.addOption([]const u8, "version", version);
    server_mod.addOptions("build_options", options);

    const ghostty = b.dependency("ghostty", .{
        .target = target,
        .optimize = optimize,
        .@"emit-lib-vt" = true,
    });
    server_mod.addImport("ghostty-vt", ghostty.module("ghostty-vt"));

    const vaxis = b.dependency("vaxis", .{
        .target = target,
        .optimize = optimize,
    });
    server_mod.addImport("vaxis", vaxis.module("vaxis"));

    const server_exe = b.addExecutable(.{
        .name = "tuios-server",
        .root_module = server_mod,
    });
    b.installArtifact(server_exe);

    // -- Client (TUI) executable --
    const client_mod = b.createModule(.{
        .root_source_file = b.path("src/client_main.zig"),
        .target = target,
        .optimize = optimize,
    });
    client_mod.addOptions("build_options", options);
    client_mod.addImport("vaxis", vaxis.module("vaxis"));
    client_mod.addImport("ghostty-vt", ghostty.module("ghostty-vt"));

    const client_exe = b.addExecutable(.{
        .name = "tuios",
        .root_module = client_mod,
    });
    b.installArtifact(client_exe);

    // -- Run steps --
    const run_server = b.addRunArtifact(server_exe);
    run_server.step.dependOn(b.getInstallStep());
    if (b.args) |args| run_server.addArgs(args);
    const run_server_step = b.step("serve", "Run the tuios server");
    run_server_step.dependOn(&run_server.step);

    const run_client = b.addRunArtifact(client_exe);
    run_client.step.dependOn(b.getInstallStep());
    if (b.args) |args| run_client.addArgs(args);
    const run_step = b.step("run", "Run the tuios client");
    run_step.dependOn(&run_client.step);

    // -- Tests --
    const server_tests = b.addTest(.{ .root_module = server_mod });
    const client_tests = b.addTest(.{ .root_module = client_mod });
    const test_step = b.step("test", "Run tests");
    test_step.dependOn(&b.addRunArtifact(server_tests).step);
    test_step.dependOn(&b.addRunArtifact(client_tests).step);
}

fn getVersion(b: *std.Build) []const u8 {
    var code: u8 = undefined;
    const git_describe = b.runAllowFailure(&.{ "git", "describe", "--match", "v*.*.*", "--tags" }, &code, .Ignore) catch {
        return "0.1.0-dev";
    };
    return std.mem.trim(u8, git_describe, " \n\r");
}
