//! tuios-gpui: a native, GPU-drawn client for the tuios terminal window
//! manager. See README.md.

mod app;
mod assets;
mod boxdraw;
mod config;
mod control;
mod fleet;
mod keys;
mod painter;
mod palette;
mod pane;
mod perf;
mod rowplan;
mod stats;
mod theme;

use gpui::{App, AppContext, Bounds, TextRenderingMode, TitlebarOptions, WindowBounds, WindowOptions, px, size};
use std::path::PathBuf;

fn usage() -> ! {
    eprintln!(
        "Usage: tuios-gpui [options]

Options:
  --tuios PATH        The tuios binary (default: tuios on PATH)
  --session NAME      The session to attach, created when missing
  --isolate DIR       Run against a private daemon whose files live in DIR
  --font FAMILY       Terminal font (default: JetBrains Mono, bundled)
  --font-size N       Terminal font size in pixels (default: 15)
  --theme NAME        A tuios theme (default: the one tuios's config names)
  --ligatures         Turn on programming ligatures
  --no-ligatures      Turn off programming ligatures (the default)
  --show-fps          Show paint timings in the status bar
  --control PATH      Accept test commands on a unix socket at PATH
  --perf              Run the performance harness and print the results
  --perf-out FILE     Also write the results to FILE as JSON"
    );
    std::process::exit(2)
}

/// GPUI reads this once, when it builds the renderer: extra stem weight for
/// grayscale text, so it looks as solid as subpixel text without the colour
/// fringes (docs/PERF-AUDIT.md, finding 1).
const CONTRAST_VAR: &str = "ZED_FONTS_GRAYSCALE_ENHANCED_CONTRAST";

fn main() {
    let file = config::load_gui();
    let mut cfg = app::Config {
        tuios: PathBuf::from("tuios"),
        session: None,
        env: Vec::new(),
        font_family: file.font_family.clone().unwrap_or_else(|| config::TERMINAL_FONTS[0].into()),
        font_size: file.font_size.unwrap_or(15.),
        line_height: file.line_height.unwrap_or(1.333),
        ligatures: file.ligatures.unwrap_or(false),
        theme: file.theme.clone().or_else(config::tuios_theme),
        ui_font: file.ui_font_family.clone().unwrap_or_else(|| config::UI_FONTS[0].into()),
        scrollback: file.scrollback.unwrap_or(pane::SCROLLBACK).clamp(100, 100_000),
        reduce_motion: file.reduce_motion.unwrap_or(false),
        show_fps: false,
        control: None,
    };
    let subpixel = file.text_antialias.as_deref() == Some("subpixel");
    // The user's own setting of the variable wins over the config.
    let user_contrast = std::env::var_os(CONTRAST_VAR).is_some();
    if !user_contrast {
        let contrast = file.text_contrast.unwrap_or(2.0).clamp(0., 4.);
        // SAFETY: nothing else runs yet; GPUI and the bridge start below.
        unsafe { std::env::set_var(CONTRAST_VAR, contrast.to_string()) };
    }
    let mut perf = false;
    let mut perf_out: Option<PathBuf> = None;
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        let mut val = || args.next().unwrap_or_else(|| usage());
        match a.as_str() {
            "--tuios" => cfg.tuios = PathBuf::from(val()),
            "--session" => cfg.session = Some(val()),
            "--isolate" => {
                let env = isolated_env(&PathBuf::from(val()));
                if let Some((_, h)) = env.iter().find(|(k, _)| k == "HOME") {
                    // Paths under the private daemon's home show as `~`.
                    fleet::set_home(h);
                }
                cfg.env.extend(env);
            }
            "--font" => cfg.font_family = val(),
            "--font-size" => cfg.font_size = val().parse().unwrap_or_else(|_| usage()),
            "--theme" => cfg.theme = Some(val()),
            "--no-ligatures" => cfg.ligatures = false,
            "--ligatures" => cfg.ligatures = true,
            "--show-fps" => cfg.show_fps = true,
            "--perf" => perf = true,
            "--control" => cfg.control = Some(PathBuf::from(val())),
            "--perf-out" => perf_out = Some(PathBuf::from(val())),
            "-h" | "--help" => usage(),
            _ => usage(),
        }
    }

    // Programs in panes must not inherit the GUI's own text setting.
    if !user_contrast {
        cfg.env.push((CONTRAST_VAR.into(), String::new()));
    }
    // The integrated GPU only, by default when it drives the displays: the
    // Vulkan loader then never loads the discrete GPU's driver, which saves
    // about 5 MB of heap and 100 MB of mappings. `gpu = "any"` keeps every
    // driver. Panes keep the user's own setting.
    let gpu = file.gpu.as_deref().unwrap_or("auto");
    if (gpu == "integrated" || gpu == "auto") && std::env::var_os("VK_DRIVER_FILES").is_none() {
        let drivers = config::integrated_vulkan_drivers(gpu == "auto");
        if drivers.is_empty() {
            if gpu == "integrated" {
                eprintln!("tuios-gpui: gpu = \"integrated\", but no integrated GPU with a Vulkan driver was found");
            }
        } else {
            let list = std::env::join_paths(&drivers).unwrap_or_default();
            // SAFETY: nothing else runs yet.
            unsafe { std::env::set_var("VK_DRIVER_FILES", &list) };
            cfg.env.push(("VK_DRIVER_FILES".into(), String::new()));
        }
    }

    gpui_platform::application().with_assets(assets::Assets).run(move |cx: &mut App| {
        cx.set_text_rendering_mode(if subpixel { TextRenderingMode::Subpixel } else { TextRenderingMode::Grayscale });
        let fonts = assets::FONTS.iter().map(|b| std::borrow::Cow::Borrowed(*b)).collect();
        if let Err(e) = cx.text_system().add_fonts(fonts) {
            eprintln!("tuios-gpui: cannot load the bundled fonts: {e}");
        }
        let bounds = Bounds::centered(None, size(px(1440.), px(900.)), cx);
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
