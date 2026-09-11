package vt

import (
	"sync"
	"testing"
)

// TestScrollbackConcurrentLine reproduce el race que tumbaba al daemon:
// dos (o más) capturadores llamando Line() a la vez — como hacen dos
// `wait-for window-output` solapados. Line() decodifica y cachea en el mapa
// sb.cache; sin lock, dos lectores lo escriben a la vez y el runtime de Go
// aborta el proceso con "concurrent map writes".
//
// Con el fix (cacheMu) corre limpio bajo -race.
func TestScrollbackConcurrentLine(t *testing.T) {
	sb := NewScrollback(10000)
	for i := 0; i < 3000; i++ {
		sb.PushBlankLine(80)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < sb.Len(); i++ {
				_ = sb.Line(i)
			}
		}()
	}
	wg.Wait()
}
