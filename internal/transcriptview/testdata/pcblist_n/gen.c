// gen.c writes the net.inet.tcp.pcblist_n fixtures for pcblistOwner, from
// the record structs of XNU (apple-oss-distributions/xnu, main on
// 2026-10-19): struct xinpgen and xinpcb_n in bsd/netinet/in_pcb.h,
// xsocket_n, xsockbuf_n and xsockstat_n in bsd/sys/socketvar.h, xtcpcb_n
// in bsd/netinet/tcp_var.h, and the record walk of get_pcblist_n in
// bsd/netinet/in_pcblist.c. The structs are declared here as XNU declares
// them, under #pragma pack(4), so the offsets come from the compiler and not
// from the parser under test. The numbers are written in host order, so run
// it on a little-endian machine, as every Mac is:
//
//   cc -o /tmp/gen gen.c && /tmp/gen
//
// xsocket_n_old is the struct of XNU 8019 (macOS 12) and before, which ends at
// so_e_pid. The fields the parser reads are at the same offsets in both.
#include <arpa/inet.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define XSO_SOCKET 0x001
#define XSO_RCVBUF 0x002
#define XSO_SNDBUF 0x004
#define XSO_STATS 0x008
#define XSO_INPCB 0x010
#define XSO_TCPCB 0x020
#define ROUNDUP64(x) (((x) + 7) & ~(size_t)7)

#pragma pack(4)
struct in_addr_4in6 { uint32_t ia46_pad32[3]; uint32_t ia46_addr4; };
struct in6_addr_ { union { uint8_t a8[16]; uint32_t a32[4]; } u; };
struct xinpgen { uint32_t xig_len; unsigned xig_count; uint64_t xig_gen; uint64_t xig_sogen; };
struct xinpcb_n {
	uint32_t xi_len, xi_kind; uint64_t xi_inpp;
	uint16_t inp_fport, inp_lport;
	uint64_t inp_ppcb, inp_gencnt;
	int inp_flags; uint32_t inp_flow;
	uint8_t inp_vflag, inp_ip_ttl, inp_ip_p;
	union { struct in_addr_4in6 inp46_foreign; struct in6_addr_ inp6_foreign; } inp_dependfaddr;
	union { struct in_addr_4in6 inp46_local; struct in6_addr_ inp6_local; } inp_dependladdr;
	struct { uint8_t inp4_ip_tos; } inp_depend4;
	struct { uint8_t inp6_hlim; int inp6_cksum; uint16_t inp6_ifindex; short inp6_hops; } inp_depend6;
	uint32_t inp_flowhash, inp_flags2;
};
#define XSOCKET_N_HEAD \
	uint32_t xso_len, xso_kind; uint64_t xso_so; \
	short so_type; uint32_t so_options; short so_linger, so_state; \
	uint64_t so_pcb; int xso_protocol, xso_family; \
	short so_qlen, so_incqlen, so_qlimit, so_timeo; uint16_t so_error; \
	int32_t so_pgid; uint32_t so_oobmark; uint32_t so_uid; int32_t so_last_pid, so_e_pid;
struct xsocket_n { XSOCKET_N_HEAD
	uint64_t so_gencnt; uint32_t so_flags, so_flags1; int32_t so_usecount, so_retaincnt; uint32_t xso_filter_flags; };
struct xsocket_n_old { XSOCKET_N_HEAD };
struct xsockbuf_n { uint32_t xsb_len, xsb_kind, sb_cc, sb_hiwat, sb_mbcnt, sb_mbmax; int32_t sb_lowat; short sb_flags, sb_timeo; };
struct data_stats { uint64_t rxpackets, rxbytes, txpackets, txbytes; };
struct xsockstat_n { uint32_t xst_len, xst_kind; struct data_stats xst_tc_stats[4]; };
struct xtcpcb_n {
	uint32_t xt_len, xt_kind; uint64_t t_segq; int t_dupacks; int t_timer[4];
	int t_state; unsigned t_flags; int t_force;
	uint32_t snd_una, snd_max, snd_nxt, snd_up, snd_wl1, snd_wl2, iss, irs;
	uint32_t rcv_nxt, rcv_adv, rcv_wnd, rcv_up, snd_wnd, snd_cwnd, snd_ssthresh;
	unsigned t_maxopd; uint32_t t_rcvtime, t_starttime; int t_rtttime; uint32_t t_rtseq;
	int t_rxtcur; unsigned t_maxseg; int t_srtt, t_rttvar, t_rxtshift; unsigned t_rttmin;
	uint32_t t_rttupdated, max_sndwnd; int t_softerror;
	char t_oobflags, t_iobc; uint8_t snd_scale, rcv_scale, request_r_scale, requested_s_scale;
	uint32_t ts_recent, ts_recent_age, last_ack_sent, cc_send, cc_recv, snd_recover, snd_cwnd_prev, snd_ssthresh_prev;
};
#pragma pack()

enum { PORT = 49374, PEER = 51234, UID = 501 };

struct sock {
	uint16_t lport, fport;
	uint32_t laddr, faddr; // host order, as inet_addr gives the reverse
	uint8_t vflag;
	uint32_t uid;
	int nosocket;
	int state;
};

static unsigned char buf[1 << 16];
static size_t n;

static void put(const void *p, size_t len) {
	memset(buf + n, 0, ROUNDUP64(len));
	memcpy(buf + n, p, len);
	n += ROUNDUP64(len);
}

static void gen(void) {
	struct xinpgen g = {sizeof g, 0, 7, 9};
	put(&g, sizeof g);
}

static void group(struct sock s, int old) {
	struct xinpcb_n xi = {0};
	xi.xi_len = sizeof xi; xi.xi_kind = XSO_INPCB; xi.xi_inpp = 0x1111;
	xi.inp_lport = htons(s.lport); xi.inp_fport = htons(s.fport);
	xi.inp_vflag = s.vflag; xi.inp_ip_p = 6;
	xi.inp_dependladdr.inp46_local.ia46_addr4 = htonl(s.laddr);
	xi.inp_dependfaddr.inp46_foreign.ia46_addr4 = htonl(s.faddr);
	put(&xi, sizeof xi);
	struct xsocket_n so = {0};
	so.xso_len = old ? sizeof(struct xsocket_n_old) : sizeof so; so.xso_kind = XSO_SOCKET;
	if (!s.nosocket) {
		so.xso_so = 0x2222; so.so_type = 1; so.xso_protocol = 6; so.xso_family = 2;
		so.so_uid = s.uid; so.so_last_pid = 4242; so.so_e_pid = 4242;
	}
	put(&so, so.xso_len);
	struct xsockbuf_n sb = {sizeof sb, XSO_RCVBUF};
	put(&sb, sizeof sb);
	sb.xsb_kind = XSO_SNDBUF;
	put(&sb, sizeof sb);
	struct xsockstat_n st = {sizeof st, XSO_STATS};
	put(&st, sizeof st);
	struct xtcpcb_n xt = {sizeof xt, XSO_TCPCB};
	xt.t_state = s.state;
	put(&xt, sizeof xt);
}

static void emit(const char *name) {
	FILE *f = fopen(name, "wb");
	fwrite(buf, 1, n, f);
	fclose(f);
	n = 0;
}

int main(void) {
	const uint32_t lo = 0x7f000001;
	struct sock listener = {PORT, 0, lo, 0, 1, UID, 0, 1};
	struct sock client = {PEER, PORT, lo, lo, 1, UID, 0, 4};
	struct sock server = {PORT, PEER, lo, lo, 1, UID, 0, 4};
	struct sock other = {443, 60000, 0x0a000002, 0x5db8d822, 1, 0, 0, 4};
	struct sock v6 = {PORT, PEER, 0, 0, 2, UID + 1, 0, 4};

	// The connection, its client side and its listener, all this user's.
	gen(); group(other, 0); group(listener, 0); group(client, 0); group(server, 0); group(v6, 0); gen();
	emit("mine.bin");

	// Another user's listener took the port: the server side is theirs.
	struct sock theirs = server; theirs.uid = UID + 1;
	struct sock theirsL = listener; theirsL.uid = UID + 1;
	gen(); group(theirsL, 0); group(client, 0); group(theirs, 0); gen();
	emit("other.bin");

	// The server side is not in the table yet.
	gen(); group(listener, 0); group(client, 0); gen();
	emit("absent.bin");

	// Only a pcb with no socket matches, as one in TIME_WAIT.
	struct sock tw = server; tw.nosocket = 1; tw.state = 10;
	gen(); group(client, 0); group(tw, 0); gen();
	emit("timewait.bin");

	// An older macOS: the shorter xsocket_n.
	gen(); group(listener, 1); group(client, 1); group(server, 1); gen();
	emit("old.bin");
	return 0;
}
