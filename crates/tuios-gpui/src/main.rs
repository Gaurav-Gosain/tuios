//! tuios-gpui: a native, GPU-drawn client for the tuios terminal window
//! manager. See README.md.

mod app;
mod boxdraw;
mod config;
mod control;
mod keys;
mod painter;
mod palette;
mod pane;
mod perf;
mod rowplan;
mod stats;
mod theme;

use gpui::{App, AppContext, Bounds, TitlebarOptions, WindowBounds, WindowOptions, px, size};
use std::path::PathBuf;

fn usage() -> ! {
    eprintln!(
        "Usage: tuios-gpui [options]

Options:
  --tuios PATH        The tuios binary (default: tuios on PATH)
  --session NAME      The session to attach, created when missing
  --isolate DIR       Run against a private daemon whose files live in DIR
  --font FAMILY       Terminal font (default: JetBrainsMono Nerd Font Mono)
  --font-size N       Terminal font size in points (default: 14)
  --theme NAME        A tuios theme (default: the one tuios's config names)
  --no-ligatures      Turn off programming ligatures
  --show-fps          Show paint timings in the status bar
  --control PATH      Accept test commands on a unix socket at PATH
  --perf              Run the performance harness and print the results
  --perf-out FILE     Also write the results to FILE as JSON"
    );
    std::process::exit(2)
}

fn main() {
    let file = config::load_gui();
    let mut cfg = app::Config {
        tuios: PathBuf::from("tuios"),
        session: None,
        env: Vec::new(),
        font_family: file.font_family.clone().unwrap_or_else(|| config::TERMINAL_FONTS[0].into()),
        font_size: file.font_size.unwrap_or(14.),
        line_height: file.line_height.unwrap_or(1.3),
        ligatures: file.ligatures.unwrap_or(true),
        theme: file.theme.clone().or_else(config::tuios_theme),
        ui_font: file.ui_font_family.clone().unwrap_or_else(|| config::UI_FONTS[0].into()),
        show_fps: false,
        control: None,
    };
    let mut perf = false;
    let mut perf_out: Option<PathBuf> = None;
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        let mut val = || args.next().unwrap_or_else(|| usage());
        match a.as_str() {
            "--tuios" => cfg.tuios = PathBuf::from(val()),
            "--session" => cfg.session = Some(val()),
            "--isolate" => cfg.env = isolated_env(&PathBuf::from(val())),
            "--font" => cfg.font_family = val(),
            "--font-size" => cfg.font_size = val().parse().unwrap_or_else(|_| usage()),
            "--theme" => cfg.theme = Some(val()),
            "--no-ligatures" => cfg.ligatures = false,
            "--show-fps" => cfg.show_fps = true,
            "--perf" => perf = true,
            "--control" => cfg.control = Some(PathBuf::from(val())),
            "--perf-out" => perf_out = Some(PathBuf::from(val())),
            "-h" | "--help" => usage(),
            _ => usage(),
        }
    }

    gpui_platform::application().run(move |cx: &mut App| {
        let bounds = Bounds::centered(None, size(px(1400.), px(880.)), cx);
        let options = WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            titlebar: Some(TitlebarOptions { title: Some("tuios".into()), ..Default::default() }),
            app_id: Some("dev.tuios.gpui".into()),
            window_min_size: Some(size(px(480.), px(320.))),
            ..Default::default()
        };
        if perf {
            let cfg = cfg.clone();
            let out = perf_out.clone();
            cx.open_window(options, |window, cx| cx.new(|cx| perf::PerfView::new(cfg, out, window, cx))).expect("open window");
        } else {
            cx.open_window(options, |window, cx| cx.new(|cx| app::TuiosApp::new(cfg.clone(), window, cx))).expect("open window");
        }
        cx.on_window_closed(|cx, _| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();
        cx.activate(true);
    });
}

/// Environment for a private tuios daemon rooted at `dir`: its own runtime
/// directory (and so its own socket), config, state and home.
fn isolated_env(dir: &std::path::Path) -> Vec<(String, String)> {
    let mut env = Vec::new();
    for (k, sub) in [
        ("XDG_RUNTIME_DIR", "run"),
        ("XDG_CONFIG_HOME", "config"),
        ("XDG_STATE_HOME", "state"),
        ("XDG_CACHE_HOME", "cache"),
        ("XDG_DATA_HOME", "data"),
        ("HOME", "home"),
    ] {
        let p = dir.join(sub);
        let _ = std::fs::create_dir_all(&p);
        if sub == "run" {
            use std::os::unix::fs::PermissionsExt;
            let _ = std::fs::set_permissions(&p, std::fs::Permissions::from_mode(0o700));
        }
        env.push((k.to_string(), p.display().to_string()));
    }
    env.push(("TUIOS_SOCKET".into(), String::new()));
    env
}
