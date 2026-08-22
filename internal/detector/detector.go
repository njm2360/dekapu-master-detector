package detector

import (
	"regexp"
	"strings"
	"time"
)

type State int

const (
	StateUnknown State = iota
	StateNotMaster
	StateMaster
)

func (s State) String() string {
	switch s {
	case StateNotMaster:
		return "NOT_MASTER"
	case StateMaster:
		return "MASTER"
	default:
		return "UNKNOWN"
	}
}

type EventType int

const (
	EventInitialMaster EventType = iota
	EventTakeover
	EventDemoted
)

func (t EventType) String() string {
	switch t {
	case EventInitialMaster:
		return "initial master"
	case EventTakeover:
		return "takeover"
	case EventDemoted:
		return "demoted"
	default:
		return "unknown"
	}
}

type Event struct {
	Type EventType
	Time time.Time
}

// 復元行の直後にSanity check行が続くのは自分がマスターの間だけ、という実ログで検証済みの法則に基づいて判定する。
// 例外は自分の入室直後のバースト区間のみで、これはマスクで除外する。
const (
	maskReleaseAfter = 120 * time.Second // 復元行がこの間なければバースト終了とみなす
	sanityWindow     = 10 * time.Second  // 復元行にSanity行が追随したとみなす最大間隔
	takeoverStreak   = 3                 // 下げると検知は速いが誤検知が増える
)

var restoreLineRe = regexp.MustCompile(`^\[Behaviour\] All \d+ bunches for DekapuPersistenceData collected, now restoring\.`)

type Detector struct {
	state State

	// 入室直後は既存プレイヤー全員分の復元+Sanityがマスター状態と無関係にバーストで流れるため、その間の判定を止める
	maskOn        bool
	lastRestoreAt time.Time

	streak  int
	windows []time.Time // 開いている10秒窓の期限(生成順)

	now time.Time

	// Resetでも保持する
	OnEvent func(Event)
}

func New() *Detector {
	return &Detector{}
}

func (d *Detector) Reset() {
	*d = Detector{OnEvent: d.OnEvent}
}

func (d *Detector) State() State { return d.state }

func (d *Detector) IsMaster() bool { return d.state == StateMaster }

func (d *Detector) Feed(ts time.Time, line string) {
	d.advanceTo(ts)

	switch {
	case strings.HasPrefix(line, "[Behaviour] I am MASTER"):
		d.initSession(ts, true)
	case strings.HasPrefix(line, "[Behaviour] I am *NOT* MASTER"):
		d.initSession(ts, false)
	case restoreLineRe.MatchString(line):
		d.onRestore(ts)
	case strings.HasPrefix(line, "[Behaviour] Sanity check"):
		d.onSanity(ts)
	case strings.HasPrefix(line, "[Behaviour] OnMasterClientSwitched"):
		d.onMasterSwitched(ts)
	}
}

func (d *Detector) Tick(now time.Time) {
	d.advanceTo(now)
}

func (d *Detector) advanceTo(t time.Time) {
	if t.After(d.now) {
		d.now = t
	}
	n := 0
	for _, deadline := range d.windows {
		if d.now.After(deadline) {
			d.streak = 0
		} else {
			d.windows[n] = deadline
			n++
		}
	}
	d.windows = d.windows[:n]
	if d.maskOn && d.now.Sub(d.lastRestoreAt) >= maskReleaseAfter {
		d.maskOn = false
	}
}

func (d *Detector) initSession(ts time.Time, master bool) {
	if master {
		d.state = StateMaster
	} else {
		d.state = StateNotMaster
	}
	d.maskOn = true
	d.lastRestoreAt = ts
	d.streak = 0
	d.windows = nil
	if master {
		d.emit(EventInitialMaster, ts)
	}
}

func (d *Detector) onRestore(ts time.Time) {
	if d.state == StateUnknown {
		return
	}
	// マスク解除タイマーの起点は状態を問わず更新する
	d.lastRestoreAt = ts
	if d.state == StateNotMaster && !d.maskOn {
		d.windows = append(d.windows, ts.Add(sanityWindow))
	}
}

func (d *Detector) onSanity(ts time.Time) {
	if d.state != StateNotMaster || d.maskOn || len(d.windows) == 0 {
		return
	}
	// Sanity 1行で開いている全窓をヒット確定して閉じる
	d.streak += len(d.windows)
	d.windows = nil
	if d.streak >= takeoverStreak {
		d.state = StateMaster
		d.streak = 0
		d.emit(EventTakeover, ts)
	}
}

func (d *Detector) onMasterSwitched(ts time.Time) {
	if d.state != StateMaster {
		return
	}
	// マスターは自分が退出するまで移らないはずなので、MASTER中にこの行が来た時点で自己認識の誤りが確定する。
	// 入室時の "I am MASTER" 自体が実態と食い違っていた実例があるため、初期状態がMASTERでもこの検出は有効。
	// バーストマスクは再セットしない
	d.state = StateNotMaster
	d.streak = 0
	d.windows = nil
	d.emit(EventDemoted, ts)
}

func (d *Detector) emit(t EventType, ts time.Time) {
	if d.OnEvent != nil {
		d.OnEvent(Event{Type: t, Time: ts})
	}
}
