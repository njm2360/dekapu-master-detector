package detector

import (
	"testing"
	"time"
)

var base = time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

const (
	lineMaster    = "[Behaviour] I am MASTER"
	lineNotMaster = "[Behaviour] I am *NOT* MASTER"
	lineRestore   = "[Behaviour] All 8 bunches for DekapuPersistenceData collected, now restoring."
	lineSanity    = "[Behaviour] Sanity check <color=green>passed</color> for ID: 0, Path: 3144"
	lineSwitched  = "[Behaviour] OnMasterClientSwitched"
)

func newWithEvents() (*Detector, *[]Event) {
	d := New()
	events := &[]Event{}
	d.OnEvent = func(e Event) { *events = append(*events, e) }
	return d, events
}

func wantEvents(t *testing.T, got []Event, want ...Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Cause != want[i].Cause || got[i].From != want[i].From || got[i].To != want[i].To {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

// マスク解除済みの NOT_MASTER セッションを作るヘルパー。開始イベントは消費済み
func notMasterUnmasked(t *testing.T) (*Detector, *[]Event) {
	t.Helper()
	d, ev := newWithEvents()
	d.Feed(at(0), lineNotMaster)
	d.Tick(at(130)) // 復元行の無音120秒でマスク解除
	if d.State() != StateNotMaster {
		t.Fatalf("state = %v, want NOT_MASTER", d.State())
	}
	wantEvents(t, *ev, Event{Cause: CauseSessionStart, From: StateUnknown, To: StateNotMaster})
	*ev = (*ev)[:0]
	return d, ev
}

func TestInitialMaster(t *testing.T) {
	d, ev := newWithEvents()
	d.Feed(at(0), lineMaster)
	d.Feed(at(0), "Actor Nr: 3")
	if !d.IsMaster() {
		t.Fatal("want MASTER")
	}
	wantEvents(t, *ev, Event{Cause: CauseSessionStart, From: StateUnknown, To: StateMaster})
}

func TestTakeoverByThreeStreak(t *testing.T) {
	d, ev := notMasterUnmasked(t)
	d.Feed(at(200), lineRestore)
	d.Feed(at(205), lineSanity)
	d.Feed(at(300), lineRestore)
	d.Feed(at(302), lineSanity)
	if d.IsMaster() {
		t.Fatal("must not become MASTER at streak 2")
	}
	d.Feed(at(400), lineRestore)
	d.Feed(at(401), lineSanity)
	if !d.IsMaster() {
		t.Fatal("want MASTER at streak 3")
	}
	wantEvents(t, *ev, Event{Cause: CauseTakeover, From: StateNotMaster, To: StateMaster})
}

func TestBurstMaskBlocksJudgment(t *testing.T) {
	d, _ := newWithEvents()
	d.Feed(at(0), lineNotMaster)
	// 入室直後バースト: マスク中は何度追随しても奪取しない
	for i := range 5 {
		d.Feed(at(1+i*2), lineRestore)
		d.Feed(at(2+i*2), lineSanity)
	}
	if d.IsMaster() {
		t.Fatal("must not become MASTER while masked")
	}
}

func TestMaskExtendedByRestoreLines(t *testing.T) {
	d, _ := newWithEvents()
	d.Feed(at(0), lineNotMaster)
	d.Feed(at(100), lineRestore) // 無音タイマーの起点更新
	d.Tick(at(130))              // セッション開始から130秒だが直近復元から30秒 → まだマスク中
	d.Feed(at(135), lineRestore)
	d.Feed(at(136), lineSanity)
	d.Feed(at(140), lineRestore)
	d.Feed(at(141), lineSanity)
	d.Feed(at(145), lineRestore)
	d.Feed(at(146), lineSanity)
	if d.IsMaster() {
		t.Fatal("mask should still be extended")
	}
}

func TestWindowTimeoutResetsStreak(t *testing.T) {
	d, _ := notMasterUnmasked(t)
	d.Feed(at(200), lineRestore)
	d.Feed(at(205), lineSanity) // streak 1
	d.Feed(at(300), lineRestore)
	d.Feed(at(303), lineSanity) // streak 2
	d.Feed(at(400), lineRestore)
	d.Tick(at(411)) // 10秒超過 → streak 0
	d.Feed(at(500), lineRestore)
	d.Feed(at(501), lineSanity) // streak 1
	d.Feed(at(600), lineRestore)
	d.Feed(at(601), lineSanity) // streak 2
	if d.IsMaster() {
		t.Fatal("window timeout should reset the streak")
	}
}

func TestMultipleConcurrentWindows(t *testing.T) {
	d, ev := notMasterUnmasked(t)
	// 同時入室: 復元行2本が併存 → Sanity 1本で両窓ヒット(+2)
	d.Feed(at(200), lineRestore)
	d.Feed(at(203), lineRestore)
	d.Feed(at(205), lineSanity)
	if d.IsMaster() {
		t.Fatal("must not become MASTER at streak 2")
	}
	d.Feed(at(300), lineRestore)
	d.Feed(at(301), lineSanity) // streak 3 → 奪取
	if !d.IsMaster() {
		t.Fatal("want MASTER at streak 3")
	}
	wantEvents(t, *ev, Event{Cause: CauseTakeover, From: StateNotMaster, To: StateMaster})
}

func TestSanityWithoutRestoreIgnored(t *testing.T) {
	d, _ := notMasterUnmasked(t)
	for i := range 10 {
		d.Feed(at(200+i), lineSanity)
	}
	if d.IsMaster() {
		t.Fatal("sanity lines without a preceding restore must be ignored")
	}
}

func TestSanityGroupCountsOnce(t *testing.T) {
	d, _ := notMasterUnmasked(t)
	// 1復元行 + Sanity 5行グループ → +1 のみ(最初の1行で窓が閉じる)
	d.Feed(at(200), lineRestore)
	for range 5 {
		d.Feed(at(202), lineSanity)
	}
	d.Feed(at(300), lineRestore)
	for range 5 {
		d.Feed(at(302), lineSanity)
	}
	if d.IsMaster() {
		t.Fatal("a sanity group must count as one hit per window")
	}
}

func TestDemotionOnMasterSwitched(t *testing.T) {
	d, ev := newWithEvents()
	d.Feed(at(0), lineMaster)
	d.Feed(at(10), lineSwitched)
	if d.IsMaster() {
		t.Fatal("want demotion")
	}
	wantEvents(t, *ev,
		Event{Cause: CauseSessionStart, From: StateUnknown, To: StateMaster},
		Event{Cause: CauseDemoted, From: StateMaster, To: StateNotMaster},
	)
}

func TestMasterSwitchedIgnoredWhileNotMaster(t *testing.T) {
	d, ev := notMasterUnmasked(t)
	d.Feed(at(200), lineSwitched)
	if d.State() != StateNotMaster || len(*ev) != 0 {
		t.Fatalf("switch between others must be ignored: state=%v events=%v", d.State(), *ev)
	}
}

func TestSessionReinitFromMaster(t *testing.T) {
	d, ev := newWithEvents()
	d.Feed(at(0), lineMaster)
	d.Feed(at(100), lineNotMaster) // Rejoin等でセッション再初期化
	if d.State() != StateNotMaster {
		t.Fatalf("state = %v, want NOT_MASTER", d.State())
	}
	wantEvents(t, *ev,
		Event{Cause: CauseSessionStart, From: StateUnknown, To: StateMaster},
		Event{Cause: CauseSessionStart, From: StateMaster, To: StateNotMaster},
	)
}

func TestSessionStartEmitsEvenWhenStateUnchanged(t *testing.T) {
	d, ev := notMasterUnmasked(t)
	d.Feed(at(200), lineNotMaster)
	wantEvents(t, *ev, Event{Cause: CauseSessionStart, From: StateNotMaster, To: StateNotMaster})
}

func TestUnrelatedLinesIgnored(t *testing.T) {
	d, _ := notMasterUnmasked(t)
	lines := []string{
		"[Behaviour] OnConnectedToMaster",
		"[Behaviour] Connected to master in jp",
		"[Behaviour] Waiting to discover master client.",
		"[Ping] Master responding: False",
		"[Set MasterVersion] Version matched!",
		"Could not locate view with ID 3427 for sanity check!",
	}
	for i, l := range lines {
		d.Feed(at(200+i), l)
	}
	if d.State() != StateNotMaster {
		t.Fatalf("state changed by an unrelated line: %v", d.State())
	}
	// 窓も開いていないこと(Sanityを流しても無反応)
	d.Feed(at(250), lineSanity)
	d.Feed(at(251), lineSanity)
	d.Feed(at(252), lineSanity)
	if d.IsMaster() {
		t.Fatal("an unrelated line was treated as a restore line")
	}
}

func TestReset(t *testing.T) {
	d, _ := newWithEvents()
	d.Feed(at(0), lineMaster)
	d.Reset()
	if d.State() != StateUnknown || d.IsMaster() {
		t.Fatal("want UNKNOWN / false after Reset")
	}
}
