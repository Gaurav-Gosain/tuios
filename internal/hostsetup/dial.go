package hostsetup

import (
	"context"
	"errors"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// DialOnce opens one link to h in this process, waits for its first attempt
// to settle, and returns the report. It is `tuios hosts test`'s dial: it
// needs no daemon here and uses none of the links one holds.
func DialOnce(ctx context.Context, h federation.Host, version string) (federation.HostReport, error) {
	table, _ := federation.NewTable([]federation.Host{h})
	m := federation.New(table, federation.Options{
		Dial:            federation.SSHDialer(os.Getenv("TUIOS_SSH")),
		ClientName:      "tuios-hosts-test",
		ClientVersion:   version,
		VerbProtocol:    session.VerbProtocolVersion,
		MinVerbProtocol: session.MinVerbProtocolVersion,
	})
	m.Start(ctx)
	reports := m.Reports(ctx)
	m.Stop()
	if len(reports) == 0 {
		return federation.HostReport{}, errors.New("host " + h.Name + " did not report a state. Run 'tuios hosts' to see the links")
	}
	return reports[0], nil
}
