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

type Cause int

const (
	CauseSessionStart Cause = iota // "I am [*NOT*] MASTER" 行。From==To でも発火する
	CauseTakeover
	CauseDemoted
)

func (c Cause) String() string {
	switch c {
	case CauseSessionStart:
		return "session start"
	case CauseTakeover:
		return "takeover"
	case CauseDemoted:
		return "demoted"
	default:
		return "unknown"
	}
}

// 状態遷移1回につき1つ。Resetは呼び出し元が文脈を持つため発火しない
type Event struct {
	Cause Cause
	From  State
	To    State
	Time  time.Time
}

// 復元行の直後にSanity check行が続くのは自分がマスターの間だけ、という実ログで検証済みの法則に基づいて判定する。
// 例外は自分の入室直後のバースト区間のみで、これはマスクで除外する。
const (
	maskReleaseAfter = 120 * time.Second // 復元行がこの間なければバースト終了とみなす
	sanityWindow     = 10 * time.Second  // 復元行にSanity行が追随したとみなす最大間隔
	takeoverHits     = 3                 // 下げると検知は速いが誤検知が増える
)

var restoreLineRe = regexp.MustCompile(`^\[Behaviour\] All \d+ bunches for DekapuPersistenceData collected, now restoring\.`)

type Detector struct {
	state State

	// 入室直後は既存プレイヤー全員分の復元+Sanityがマスター状態と無関係にバーストで流れるため、その間の判定を止める
	inJoinBurst bool
	burstAnchor time.Time // 最後の復元行の時刻。無ければセッション開始時刻

	hits    int         // Sanityが追随した復元行の連続数
	pending []time.Time // 応答待ち復元行の締切(生成順=昇順)

	// ログ由来のtimestampとTickの実時刻が合流する。単調増加でのみ更新する
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
	d.expirePending()
	d.releaseBurstMask()
}

// 締切を過ぎた復元行は Sanityが続かなかった = 反証なので、連続数を白紙に戻す
func (d *Detector) expirePending() {
	kept := 0
	for _, deadline := range d.pending {
		if d.now.After(deadline) {
			d.hits = 0
			continue
		}
		d.pending[kept] = deadline
		kept++
	}
	d.pending = d.pending[:kept]
}

func (d *Detector) releaseBurstMask() {
	if d.inJoinBurst && d.now.Sub(d.burstAnchor) >= maskReleaseAfter {
		d.inJoinBurst = false
	}
}

func (d *Detector) judging() bool {
	return d.state == StateNotMaster && !d.inJoinBurst
}

func (d *Detector) initSession(ts time.Time, master bool) {
	d.inJoinBurst = true
	d.burstAnchor = ts
	d.hits = 0
	d.pending = nil
	to := StateNotMaster
	if master {
		to = StateMaster
	}
	d.transition(CauseSessionStart, to, ts)
}

func (d *Detector) onRestore(ts time.Time) {
	if d.state == StateUnknown {
		return
	}
	// マスク解除タイマーの起点は判定区間外でも更新する
	d.burstAnchor = ts
	if d.judging() {
		d.pending = append(d.pending, ts.Add(sanityWindow))
	}
}

func (d *Detector) onSanity(ts time.Time) {
	if !d.judging() || len(d.pending) == 0 {
		return
	}
	// Sanity 1行で応答待ちの全復元行をヒット確定して閉じる
	d.hits += len(d.pending)
	d.pending = nil
	if d.hits >= takeoverHits {
		d.hits = 0
		d.transition(CauseTakeover, StateMaster, ts)
	}
}

func (d *Detector) onMasterSwitched(ts time.Time) {
	if d.state != StateMaster {
		return
	}
	// マスターは自分が退出するまで移らないはずなので、MASTER中にこの行が来た時点で自己認識の誤りが確定する。
	// 入室時の "I am MASTER" 自体が実態と食い違っていた実例があるため、初期状態がMASTERでもこの検出は有効。
	// バーストマスクは再セットしない
	d.hits = 0
	d.pending = nil
	d.transition(CauseDemoted, StateNotMaster, ts)
}

func (d *Detector) transition(c Cause, to State, ts time.Time) {
	from := d.state
	d.state = to
	if d.OnEvent != nil {
		d.OnEvent(Event{Cause: c, From: from, To: to, Time: ts})
	}
}
