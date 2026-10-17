// Probe 1: kqueue EVFILT_PROC NOTE_EXIT / NOTE_EXITSTATUS on a non-child (macOS).
// Build: clang -O2 -o p1 p1_kqueue_proc.c
// Run:   ./p1
#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/event.h>
#include <sys/sysctl.h>
#include <sys/time.h>
#include <sys/wait.h>
#include <unistd.h>

static char *self;

static pid_t spawn_sh(const char *script) {
    pid_t p = fork();
    if (p == 0) { execl("/bin/sh", "sh", "-c", script, (char *)0); _exit(127); }
    return p;
}

static pid_t ppid_of(pid_t pid) {
    struct kinfo_proc kp; size_t len = sizeof kp;
    int mib[4] = {CTL_KERN, KERN_PROC, KERN_PROC_PID, pid};
    if (sysctl(mib, 4, &kp, &len, NULL, 0) != 0 || len == 0) return -1;
    return kp.kp_eproc.e_ppid;
}

// Prints one line: REGISTER_FAIL errno | EVENT fflags data exited status | TIMEOUT
static int do_watch(pid_t pid, int with_status, int timeout_ms, char *out, size_t outsz) {
    int kq = kqueue();
    struct kevent ev;
    unsigned fflags = NOTE_EXIT | (with_status ? NOTE_EXITSTATUS : 0);
    EV_SET(&ev, pid, EVFILT_PROC, EV_ADD | EV_ONESHOT, fflags, 0, NULL);
    struct timespec t0, t1; clock_gettime(CLOCK_MONOTONIC, &t0);
    if (kevent(kq, &ev, 1, NULL, 0, NULL) < 0) {
        snprintf(out, outsz, "REGISTER_FAIL errno=%d (%s)", errno, strerror(errno));
        close(kq); return -1;
    }
    struct kevent got;
    struct timespec to = {timeout_ms / 1000, (timeout_ms % 1000) * 1000000L};
    int n = kevent(kq, NULL, 0, &got, 1, &to);
    clock_gettime(CLOCK_MONOTONIC, &t1);
    long ms = (t1.tv_sec - t0.tv_sec) * 1000 + (t1.tv_nsec - t0.tv_nsec) / 1000000;
    if (n == 0) snprintf(out, outsz, "TIMEOUT after %ldms", ms);
    else if (n < 0) snprintf(out, outsz, "KEVENT_ERR errno=%d", errno);
    else {
        int st = (int)got.data;
        snprintf(out, outsz, "EVENT after %ldms fflags=0x%x flags=0x%x data=0x%lx WIFEXITED=%d WEXITSTATUS=%d",
                 ms, got.fflags, got.flags, (long)got.data, WIFEXITED(st), WIFEXITED(st) ? WEXITSTATUS(st) : -1);
    }
    close(kq); return n;
}

// Run the watcher as a separate process (C), not B's parent. Returns its output line.
static void watch_in_other_process(pid_t pid, int with_status, int timeout_ms, char *out, size_t outsz) {
    int fds[2]; pipe(fds);
    pid_t c = fork();
    if (c == 0) {
        close(fds[0]); dup2(fds[1], 1);
        char a[32], b[8], d[16];
        snprintf(a, sizeof a, "%d", pid); snprintf(b, sizeof b, "%d", with_status); snprintf(d, sizeof d, "%d", timeout_ms);
        execl(self, self, "watch", a, b, d, (char *)0); _exit(127);
    }
    close(fds[1]);
    ssize_t n = read(fds[0], out, outsz - 1); out[n > 0 ? n : 0] = 0;
    char *nl = strchr(out, '\n'); if (nl) *nl = 0;
    close(fds[0]); waitpid(c, NULL, 0);
}

static void report(const char *name, const char *line, const char *expect_substr, int want_match) {
    int match = strstr(line, expect_substr) != NULL;
    printf("%-60s %s\n    -> %s\n", name, (match == want_match) ? "PASS" : "FAIL", line);
}

int main(int argc, char **argv) {
    self = argv[0];
    if (argc == 5 && !strcmp(argv[1], "watch")) {
        char out[256]; do_watch(atoi(argv[2]), atoi(argv[3]), atoi(argv[4]), out, sizeof out);
        printf("%s\n", out); return 0;
    }
    char out[256];

    // 1a: sibling watcher, NOTE_EXIT|NOTE_EXITSTATUS, B exits 9 (0x900 expected if status delivered)
    int st;
    pid_t b = spawn_sh("sleep 2; exit 9");
    printf("[1a] B=%d parent=%d watcher is a separate process (sibling)\n", b, getpid());
    watch_in_other_process(b, 1, 5000, out, sizeof out);
    report("1a non-child, NOTE_EXIT|NOTE_EXITSTATUS: status 9 delivered?", out, "WEXITSTATUS=9", 1);
    waitpid(b, &st, 0);
    printf("    parent's own wait4: WEXITSTATUS=%d (status still reapable by parent)\n", WEXITSTATUS(st));

    // 1a': same, exit 0 -> must NOT report 9 (shows the status field actually varies)
    b = spawn_sh("sleep 1; exit 0");
    watch_in_other_process(b, 1, 5000, out, sizeof out);
    report("1a' non-child, exit 0: reports a different value than 9", out, "WEXITSTATUS=9", 0);
    waitpid(b, NULL, 0);

    // 1b: NOTE_EXIT alone
    b = spawn_sh("sleep 1; exit 9");
    watch_in_other_process(b, 0, 5000, out, sizeof out);
    report("1b non-child, NOTE_EXIT alone: event delivered?", out, "EVENT", 1);
    waitpid(b, NULL, 0);

    // 1c control: watcher's own child
    {
        pid_t own = spawn_sh("sleep 1; exit 9");
        do_watch(own, 1, 5000, out, sizeof out);
        report("1c CONTROL own child, NOTE_EXIT|NOTE_EXITSTATUS: status 9", out, "WEXITSTATUS=9", 1);
        waitpid(own, NULL, 0);
    }

    // 1h: own child already a zombie when the parent registers -> ESRCH or immediate event?
    {
        pid_t own = spawn_sh("exit 9");
        usleep(300000);
        do_watch(own, 1, 1000, out, sizeof out);
        report("1h own zombie child at register time (any outcome reported)", out, "", 1);
        waitpid(own, &st, 0);
        printf("    reaped afterwards: WEXITSTATUS=%d\n", WEXITSTATUS(st));
    }

    // 1d negative control: process that does not exit in the timeout -> TIMEOUT
    b = spawn_sh("sleep 30");
    watch_in_other_process(b, 1, 800, out, sizeof out);
    report("1d NEG CONTROL non-exiting process -> TIMEOUT", out, "TIMEOUT", 1);
    kill(b, SIGKILL); waitpid(b, NULL, 0);

    // 1e: B already a zombie (unreaped by A) when C registers
    b = spawn_sh("exit 9");
    usleep(500000);
    printf("[1e] B=%d kill(B,0)=%d (0 => zombie still exists)\n", b, kill(b, 0));
    watch_in_other_process(b, 1, 2000, out, sizeof out);
    report("1e zombie at register time (any outcome reported)", out, "", 1);
    waitpid(b, &st, 0);
    printf("    parent reaps zombie afterwards: WEXITSTATUS=%d\n", WEXITSTATUS(st));
    // 1e': already reaped (pid gone) -> expect ESRCH
    watch_in_other_process(b, 1, 1000, out, sizeof out);
    report("1e' fully reaped pid at register time -> REGISTER_FAIL", out, "REGISTER_FAIL", 1);

    // 1f: A (B's parent) exits before B; B reparents to launchd. Watcher registered before A exits.
    // 1g: same, watcher registered after reparent.
    for (int after = 0; after <= 1; after++) {
        int pp[2]; pipe(pp);
        pid_t a = fork();
        if (a == 0) {
            pid_t bb = spawn_sh(after ? "sleep 2; exit 9" : "sleep 2; exit 9");
            write(pp[1], &bb, sizeof bb); usleep(after ? 0 : 500000); _exit(0);
        }
        pid_t bb; read(pp[0], &bb, sizeof bb); close(pp[0]); close(pp[1]);
        if (after) { waitpid(a, NULL, 0); usleep(100000); printf("[1g] B=%d ppid after A exit=%d\n", bb, ppid_of(bb)); }
        else {
            // start watcher while A is still alive, then let A exit
            int fds[2]; pipe(fds);
            pid_t c = fork();
            if (c == 0) {
                close(fds[0]); dup2(fds[1], 1);
                char s[32]; snprintf(s, sizeof s, "%d", bb);
                execl(self, self, "watch", s, "1", "5000", (char *)0); _exit(127);
            }
            close(fds[1]);
            printf("[1f] B=%d ppid while A alive=%d (A=%d)\n", bb, ppid_of(bb), a);
            waitpid(a, NULL, 0); usleep(100000);
            printf("[1f] B=%d ppid after A exit=%d\n", bb, ppid_of(bb));
            ssize_t n = read(fds[0], out, sizeof out - 1); out[n > 0 ? n : 0] = 0;
            char *nl = strchr(out, '\n'); if (nl) *nl = 0;
            close(fds[0]); waitpid(c, NULL, 0);
            report("1f reparented to launchd, watch registered before: status 9", out, "WEXITSTATUS=9", 1);
            continue;
        }
        watch_in_other_process(bb, 1, 5000, out, sizeof out);
        report("1g reparented to launchd, watch registered after: status 9", out, "WEXITSTATUS=9", 1);
    }
    return 0;
}
