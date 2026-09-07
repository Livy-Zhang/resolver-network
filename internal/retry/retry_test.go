package retry
import("testing";"time")
func TestDelayIsBounded(t *testing.T){if got:=Delay(0);got!=5*time.Minute{t.Fatalf("got %v",got)};if got:=Delay(2);got!=10*time.Minute{t.Fatalf("got %v",got)};if got:=Delay(99);got!=60*time.Minute{t.Fatalf("got %v",got)}}
