package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Options configure an Agent.
type Options struct {
	Paths      Paths
	Backend    Backend
	CSMVersion string
	// OS is a short description ("linux ubuntu 24.04").
	OS       string
	Hostname string
	// Logf receives the agent's log lines; they are redacted first.
	Logf func(format string, args ...any)

	// Fetcher resolves Ready Up bundles (nil = defaults).
	Fetcher *Fetcher
	// HTTPClient is used to re-enroll with a fleet key (nil = default).
	HTTPClient *http.Client
	// Dialer overrides the WebSocket dialer (tests).
	Dialer *websocket.Dialer

	// Timings (zero = the protocol defaults).
	InventoryEvery time.Duration // 60 s
	HealthEvery    time.Duration // 5 s
	HungAfter      time.Duration // 30 s
	HelloTimeout   time.Duration // 10 s
	BackoffBase    time.Duration // 1 s
	BackoffCap     time.Duration // 30 s
	// MaxSkew is how far a command's ts may be from our clock (5 min).
	MaxSkew time.Duration
	// NotLinkedWait is how often a machine without credentials looks again.
	NotLinkedWait time.Duration
}

func (o *Options) defaults() {
	if o.InventoryEvery <= 0 {
		o.InventoryEvery = 60 * time.Second
	}
	if o.HealthEvery <= 0 {
		o.HealthEvery = 5 * time.Second
	}
	if o.HungAfter <= 0 {
		o.HungAfter = 30 * time.Second
	}
	if o.HelloTimeout <= 0 {
		o.HelloTimeout = 10 * time.Second
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = time.Second
	}
	if o.BackoffCap <= 0 {
		o.BackoffCap = 30 * time.Second
	}
	if o.MaxSkew <= 0 {
		o.MaxSkew = 5 * time.Minute
	}
	if o.NotLinkedWait <= 0 {
		o.NotLinkedWait = 30 * time.Second
	}
	if o.Fetcher == nil {
		o.Fetcher = &Fetcher{}
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
}

// maxUnacked bounds the reliable messages kept for replay.
const maxUnacked = 1000

// Agent is the host agent. Run it with Run.
type Agent struct {
	opts Options

	mu       sync.Mutex
	creds    *Credentials
	bootID   string
	streamID string
	txSeq    int64
	unacked  []*Envelope
	rxSeq    int64
	sess     *session

	// heavy serializes create / remove / update jobs; a quick command that
	// finds it taken answers busy instead of waiting behind an update.
	heavy     sync.RWMutex
	heavyName string
	heavyMu   sync.Mutex

	serverMu sync.Mutex
	serverOp map[int]string // server -> running quick command

	logsMu   sync.Mutex
	logs     map[string]context.CancelFunc
	health   *healthTracker
	invMu    sync.Mutex
	invLast  string
	invAt    time.Time
	invDirty bool
}

// New returns an agent. Nothing runs until Run.
func New(o Options) *Agent {
	o.defaults()
	a := &Agent{
		opts:     o,
		bootID:   NewULID(),
		streamID: NewULID(),
		serverOp: map[int]string{},
		logs:     map[string]context.CancelFunc{},
	}
	a.health = newHealthTracker(a)
	return a
}

func (a *Agent) logf(format string, args ...any) {
	a.opts.Logf("%s", Redact(fmt.Sprintf(format, args...)))
}

// Run connects, keeps the link up and handles messages until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	go a.health.run(ctx)

	attempt := 0
	notLinkedLogged := false
	var lastReenroll time.Time
	for ctx.Err() == nil {
		creds, err := LoadCredentials(a.opts.Paths)
		if err != nil {
			if !notLinkedLogged || !errors.Is(err, ErrNotLinked) {
				a.logf("host agent: %v", err)
				notLinkedLogged = true
			}
			if !sleepCtx(ctx, a.opts.NotLinkedWait) {
				break
			}
			continue
		}
		notLinkedLogged = false
		a.setCreds(creds)

		started := time.Now()
		code, reason, err := a.runSession(ctx, creds)
		if ctx.Err() != nil {
			break
		}
		lasted := time.Since(started)
		if lasted >= 60*time.Second {
			attempt = 0
		}
		base, capd := a.opts.BackoffBase, a.opts.BackoffCap
		var fixed time.Duration
		switch code {
		case CloseBadToken, CloseRevoked:
			a.logf("host agent: credentials rejected (%d %s); re-enroll with `csm link` or check the host on the platform", code, reason)
			if creds.FleetKey != "" && time.Since(lastReenroll) > 10*time.Minute {
				lastReenroll = time.Now()
				if err := a.reenroll(ctx, creds); err != nil {
					a.logf("host agent: re-enrolling with the fleet key failed: %v", err)
				} else {
					a.logf("host agent: re-enrolled with the fleet key")
					attempt = 0
					continue
				}
			}
			capd = 10 * time.Minute
		case CloseUnsupportedProtocol:
			a.logf("host agent: the platform does not speak protocol %d (%s); update csm", ProtocolVersion, reason)
			capd = 10 * time.Minute
		case CloseReplaced:
			a.logf("host agent: replaced by a newer session for this host (another csm agent running with the same credentials?)")
			fixed = 30 * time.Second
		case CloseRateLimited:
			fixed = retryAfter(reason, 30*time.Second)
		case ClosePlatformDraining:
			base = 2 * time.Second
		default:
			if err != nil {
				a.logf("host agent: connection: %v", err)
			} else if code != 0 {
				a.logf("host agent: connection closed (%d %s)", code, reason)
			}
		}
		delay := fixed
		if delay == 0 {
			delay = backoff(attempt, base, capd)
		}
		attempt++
		if !sleepCtx(ctx, delay) {
			break
		}
	}
	a.stopAllLogs()
	return ctx.Err()
}

func (a *Agent) setCreds(c *Credentials) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.creds == nil || a.creds.HostID != c.HostID {
		st := loadState(a.opts.Paths, c.HostID)
		a.rxSeq = st.LastRxSeq
		if a.creds != nil {
			// A new identity is a new stream.
			a.streamID = NewULID()
			a.txSeq = 0
			a.unacked = nil
		}
	}
	a.creds = c
}

func (a *Agent) currentCreds() *Credentials {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creds
}

func (a *Agent) reenroll(ctx context.Context, c *Credentials) error {
	nc, err := Enroll(ctx, EnrollOptions{
		PlatformURL: c.PlatformURL, CodeOrKey: c.FleetKey, MachineID: c.MachineID,
		Hostname: a.opts.Hostname, OS: a.opts.OS, CSMVersion: a.opts.CSMVersion,
		InsecureDev: c.InsecureDev, CAFile: c.CAFile, HTTPClient: a.opts.HTTPClient,
	})
	if err != nil {
		return err
	}
	return SaveCredentials(a.opts.Paths, nc)
}

// backoff is min(cap, base·2^attempt) with full jitter (FLEET.md §6.3).
func backoff(attempt int, base, capd time.Duration) time.Duration {
	d := base
	for i := 0; i < attempt && d < capd; i++ {
		d *= 2
	}
	if d > capd {
		d = capd
	}
	if d <= 0 {
		return base
	}
	j := time.Duration(rand.Int63n(int64(d)))
	if j < 100*time.Millisecond {
		j = 100 * time.Millisecond
	}
	return j
}

// retryAfter reads {"retry_after_ms": N} from a 4429 close reason.
func retryAfter(reason string, def time.Duration) time.Duration {
	var r struct {
		RetryAfterMS int64 `json:"retry_after_ms"`
	}
	if json.Unmarshal([]byte(reason), &r) == nil && r.RetryAfterMS > 0 && r.RetryAfterMS < int64(time.Hour/time.Millisecond) {
		return time.Duration(r.RetryAfterMS) * time.Millisecond
	}
	return def
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// --- outbound ------------------------------------------------------------------

// frame builds an envelope with our current ack. Caller holds a.mu.
func (a *Agent) frameLocked(typ string, payload any, ref string, seq int64) *Envelope {
	env := &Envelope{V: ProtocolVersion, Type: typ, ID: NewULID(), TS: time.Now().UnixMilli(), Payload: mustJSON(payload)}
	if seq > 0 {
		s := seq
		env.Seq = &s
	}
	if ref != "" {
		r := ref
		env.Ref = &r
	}
	return env
}

func (a *Agent) encodeLocked(env *Envelope) []byte {
	ack := a.rxSeq
	out := *env
	out.Ack = &ack
	b, err := json.Marshal(&out)
	if err != nil {
		panic(err)
	}
	return b
}

// sendReliable queues a reliable message: it is kept until the platform acks
// it and replayed after a reconnect.
func (a *Agent) sendReliable(typ string, payload any, ref string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.txSeq++
	env := a.frameLocked(typ, payload, ref, a.txSeq)
	a.unacked = append(a.unacked, env)
	if len(a.unacked) > maxUnacked {
		// Dropping breaks the seq chain: start a new stream so the next
		// hello is a reset (FLEET.md §6.4, §6.5).
		a.unacked = a.unacked[len(a.unacked)-maxUnacked:]
		a.streamID = NewULID()
		a.logf("host agent: outbound buffer full; dropped the oldest messages (next connect resets the stream)")
	}
	if a.sess != nil && a.sess.ready {
		a.sess.enqueue(a.encodeLocked(env), true)
	}
}

// sendEphemeral sends now when a session is up, else drops.
func (a *Agent) sendEphemeral(typ string, payload any, ref string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil || !a.sess.ready {
		return false
	}
	env := a.frameLocked(typ, payload, ref, 0)
	return a.sess.enqueue(a.encodeLocked(env), false)
}

// sendResult is the one host.result for a platform message.
func (a *Agent) sendResult(ref string, r ResultPayload) {
	if r.Output != "" {
		r.Output = tail(Redact(r.Output), 16<<10)
	}
	if r.Error != nil {
		r.Error.Message = truncate(Redact(r.Error.Message), 2000)
	}
	a.sendReliable(TypeResult, r, ref)
}

func (a *Agent) progress(ref, step string, pct int) {
	p := ProgressPayload{Ref: ref, Step: truncate(Redact(step), 256)}
	if pct >= 0 {
		if pct > 100 {
			pct = 100
		}
		p.Pct = &pct
	}
	a.sendEphemeral(TypeProgress, p, "")
}

// --- session -------------------------------------------------------------------

type session struct {
	a       *Agent
	conn    *websocket.Conn
	out     chan []byte
	done    chan struct{}
	once    sync.Once
	ready   bool // guarded by a.mu
	helloID string
	timeout time.Duration

	ackMu    sync.Mutex
	acksOwed int
	ackTimer *time.Timer

	closeCode   int
	closeReason string
}

// enqueue hands a frame to the writer. A full queue on a reliable message
// means a stuck link: the session is dropped and the message replayed later.
func (s *session) enqueue(b []byte, reliable bool) bool {
	select {
	case s.out <- b:
		return true
	default:
		if reliable {
			go s.close(CloseGoingAway, "send queue full")
		}
		return false
	}
}

func (s *session) close(code int, reason string) {
	s.once.Do(func() {
		msg := websocket.FormatCloseMessage(code, reason)
		_ = s.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(2*time.Second))
		close(s.done)
		_ = s.conn.Close()
	})
}

func (s *session) writer() {
	for {
		select {
		case <-s.done:
			return
		case b := <-s.out:
			_ = s.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if err := s.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				s.close(CloseGoingAway, "write failed")
				return
			}
		}
	}
}

// owe schedules an ack (at most 1 s or 32 messages after receipt, §5).
func (s *session) owe(now bool) {
	s.ackMu.Lock()
	s.acksOwed++
	send := now || s.acksOwed >= 32
	if send {
		s.acksOwed = 0
		if s.ackTimer != nil {
			s.ackTimer.Stop()
			s.ackTimer = nil
		}
	} else if s.ackTimer == nil {
		s.ackTimer = time.AfterFunc(time.Second, func() {
			s.ackMu.Lock()
			owed := s.acksOwed
			s.acksOwed = 0
			s.ackTimer = nil
			s.ackMu.Unlock()
			if owed > 0 {
				s.a.sendEphemeral(TypeAck, struct{}{}, "")
			}
		})
	}
	s.ackMu.Unlock()
	if send {
		s.a.sendEphemeral(TypeAck, struct{}{}, "")
	}
}

// runSession connects once and runs until the link ends. It returns the
// close code the platform sent (0 when none).
func (a *Agent) runSession(ctx context.Context, creds *Credentials) (int, string, error) {
	if _, err := CheckURL(creds.WSURL, creds.InsecureDev, "wss", "ws"); err != nil {
		return 0, "", err
	}
	dialer := a.opts.Dialer
	if dialer == nil {
		tlsCfg, err := TLSConfig(creds.CAFile)
		if err != nil {
			return 0, "", err
		}
		dialer = &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 15 * time.Second,
			TLSClientConfig:  tlsCfg,
			ReadBufferSize:   64 << 10,
			WriteBufferSize:  64 << 10,
		}
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+creds.Token)
	hdr.Set("User-Agent", "csm/"+a.opts.CSMVersion)
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	conn, resp, err := dialer.DialContext(dctx, creds.WSURL, hdr)
	cancel()
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return CloseBadToken, "HTTP 401 on upgrade", nil
			case http.StatusForbidden:
				return CloseRevoked, "HTTP 403 on upgrade", nil
			}
			return 0, "", fmt.Errorf("upgrade refused: HTTP %d", resp.StatusCode)
		}
		return 0, "", errors.New(Redact(err.Error()))
	}
	conn.SetReadLimit(MaxFrameBytes)

	s := &session{a: a, conn: conn, out: make(chan []byte, 512), done: make(chan struct{}), timeout: 30 * time.Second}
	go s.writer()
	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	go func() {
		select {
		case <-sctx.Done():
			s.close(CloseNormal, "agent stopping")
		case <-s.done:
		}
	}()

	// hello goes out before the session is registered: nothing else may
	// precede it.
	a.mu.Lock()
	hello := HelloPayload{
		HostID: creds.HostID, MachineID: creds.MachineID, TenantID: "default",
		Protocol:     ProtocolRange{Min: ProtocolVersion, Max: ProtocolVersion},
		Versions:     HostVersions{CSM: a.opts.CSMVersion, OS: truncate(a.opts.OS, 128)},
		Capabilities: Capabilities, Hostname: truncate(a.opts.Hostname, 255), BootID: a.bootID,
		Stream: StreamState{ID: a.streamID, LastTxSeq: a.txSeq, LastRxSeq: a.rxSeq},
	}
	henv := a.frameLocked(TypeHello, hello, "", 0)
	s.helloID = henv.ID
	hb := a.encodeLocked(henv)
	a.mu.Unlock()
	s.enqueue(hb, false)

	_ = conn.SetReadDeadline(time.Now().Add(a.opts.HelloTimeout))
	err = a.readLoop(sctx, s)

	a.mu.Lock()
	if a.sess == s {
		a.sess = nil
	}
	a.mu.Unlock()
	s.close(CloseNormal, "")
	s.ackMu.Lock()
	if s.ackTimer != nil {
		s.ackTimer.Stop()
	}
	s.ackMu.Unlock()
	a.stopSessionLogs()

	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		return ce.Code, ce.Text, nil
	}
	if s.closeCode != 0 {
		return s.closeCode, s.closeReason, nil
	}
	if sctx.Err() != nil {
		return 0, "", nil
	}
	return 0, "", err
}

func (a *Agent) readLoop(ctx context.Context, s *session) error {
	var pingStop chan struct{}
	defer func() {
		if pingStop != nil {
			close(pingStop)
		}
	}()
	for {
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			return err
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(s.timeout))
		if mt != websocket.TextMessage {
			s.closeCode, s.closeReason = CloseProtocolError, "binary frames are not part of the protocol"
			s.close(CloseProtocolError, s.closeReason)
			return errors.New(s.closeReason)
		}
		env, perr := ParseEnvelope(data)
		if perr != nil {
			a.logf("host agent: dropping an invalid frame: %v", perr)
			s.closeCode, s.closeReason = CloseProtocolError, truncate(perr.Error(), 100)
			s.close(CloseProtocolError, s.closeReason)
			return perr
		}

		a.mu.Lock()
		ready := s.ready
		a.mu.Unlock()
		if !ready {
			if env.Type != TypeWelcome {
				s.closeCode, s.closeReason = CloseProtocolError, "expected welcome"
				s.close(CloseProtocolError, "expected welcome")
				return errors.New("expected welcome, got " + env.Type)
			}
			var w WelcomePayload
			if err := DecodePayload(env.Payload, &w); err == nil {
				err = w.validate()
			}
			if err != nil {
				s.closeCode, s.closeReason = CloseProtocolError, "invalid welcome"
				s.close(CloseProtocolError, "invalid welcome")
				return fmt.Errorf("invalid welcome: %v", err)
			}
			a.onWelcome(s, &w)
			pingStop = make(chan struct{})
			go a.pinger(s, time.Duration(w.Heartbeat.IntervalMS)*time.Millisecond, pingStop)
			continue
		}

		if env.Ack != nil {
			a.onAck(*env.Ack)
		}
		if env.Reliable() {
			a.onReliable(ctx, s, env)
			continue
		}
		a.onEphemeral(s, env)
	}
}

func (a *Agent) onWelcome(s *session, w *WelcomePayload) {
	a.mu.Lock()
	s.timeout = time.Duration(w.Heartbeat.TimeoutMS) * time.Millisecond
	if w.Resume.Result == "resumed" {
		a.dropAckedLocked(w.Resume.PlatformLastRxSeq)
	}
	s.ready = true
	a.sess = s
	// Replay what the platform has not acked, in seq order (§6.4).
	for _, env := range a.unacked {
		s.enqueue(a.encodeLocked(env), true)
	}
	creds := a.creds
	a.mu.Unlock()
	_ = s.conn.SetReadDeadline(time.Now().Add(s.timeout))
	a.logf("host agent: online as %s (session %s, resume %s)", creds.HostID, w.SessionID, w.Resume.Result)
	go a.sendInventory(context.Background(), "", true)
}

func (a *Agent) dropAckedLocked(upTo int64) {
	i := 0
	for i < len(a.unacked) && *a.unacked[i].Seq <= upTo {
		i++
	}
	if i > 0 {
		a.unacked = append([]*Envelope(nil), a.unacked[i:]...)
	}
}

func (a *Agent) onAck(ack int64) {
	a.mu.Lock()
	a.dropAckedLocked(ack)
	a.mu.Unlock()
}

func (a *Agent) pinger(s *session, every time.Duration, stop chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-s.done:
			return
		case <-t.C:
			a.sendEphemeral(TypePing, PingPayload{T: time.Now().UnixMilli()}, "")
		}
	}
}

func (a *Agent) onEphemeral(s *session, env *Envelope) {
	switch env.Type {
	case TypePing:
		var p PingPayload
		if DecodePayload(env.Payload, &p) == nil {
			a.sendEphemeral(TypePong, PingPayload{T: p.T}, env.ID)
		}
	case TypeError:
		var p ErrorPayload
		_ = DecodePayload(env.Payload, &p)
		a.logf("host agent: platform reported an error: %s %s (%s)", truncate(p.Code, 64), truncate(p.Message, 300), truncate(p.Type, 64))
	case TypeHello:
		s.closeCode, s.closeReason = CloseProtocolError, "unexpected hello"
		s.close(CloseProtocolError, "unexpected hello")
	default:
		// pong, ack, welcome again, unknown ephemeral types: ignored (§5).
	}
}

func (a *Agent) onReliable(ctx context.Context, s *session, env *Envelope) {
	seq := *env.Seq
	a.mu.Lock()
	rx := a.rxSeq
	a.mu.Unlock()
	if seq <= rx {
		// A replay of something already handled: ack it again.
		s.owe(true)
		return
	}
	if seq != rx+1 {
		// Out of order: dropped unacked, the platform replays it.
		a.logf("host agent: platform seq %d after %d; waiting for the replay", seq, rx)
		return
	}
	a.dispatch(ctx, env)
	a.mu.Lock()
	a.rxSeq = seq
	hostID := ""
	if a.creds != nil {
		hostID = a.creds.HostID
	}
	a.mu.Unlock()
	if err := saveState(a.opts.Paths, agentState{HostID: hostID, LastRxSeq: seq}); err != nil {
		a.logf("host agent: could not save state: %v", err)
	}
	s.owe(false)
}

// dispatch handles one reliable platform message. Commands run in their own
// goroutine; this only validates and routes, so the read loop never blocks.
func (a *Agent) dispatch(ctx context.Context, env *Envelope) {
	if env.Type == TypeAuthRotate {
		a.onRotate(env)
		return
	}
	cmd := newCommand(env.Type)
	if cmd == nil {
		a.sendEphemeral(TypeError, ErrorPayload{Code: "unknown_type", Message: "unknown message type " + truncate(env.Type, 64), Type: truncate(env.Type, 128)}, env.ID)
		return
	}
	if err := DecodePayload(env.Payload, cmd); err == nil {
		err = validateCommand(env.Type, cmd)
		if err != nil {
			a.sendResult(env.ID, rejected(CodeBadArgs, err.Error()))
			return
		}
	} else {
		a.sendResult(env.ID, rejected(CodeBadArgs, err.Error()))
		return
	}
	now := time.Now()
	if exp := expiresAt(cmd); exp > 0 && now.UnixMilli() > exp {
		a.sendResult(env.ID, rejected(CodeExpired, "the command expired before it reached this host"))
		return
	}
	if skew := now.Sub(time.UnixMilli(env.TS)); skew > a.opts.MaxSkew || skew < -a.opts.MaxSkew {
		a.sendResult(env.ID, rejected(CodeStale, fmt.Sprintf("the command was sent %s from this host's clock (limit %s); it is not run. Check NTP if the clocks differ", skew.Round(time.Second), a.opts.MaxSkew)))
		return
	}
	go a.handle(context.Background(), env, cmd)
}

func (a *Agent) onRotate(env *Envelope) {
	var p AuthRotatePayload
	if err := DecodePayload(env.Payload, &p); err != nil || !hostTokenRe.MatchString(p.Token) {
		a.sendEphemeral(TypeError, ErrorPayload{Code: "invalid_payload", Message: "auth.rotate without a host token", Type: TypeAuthRotate}, env.ID)
		return
	}
	a.mu.Lock()
	c := *a.creds
	a.mu.Unlock()
	c.Token = p.Token
	c.RotatedAt = nowRFC3339()
	if err := SaveCredentials(a.opts.Paths, &c); err != nil {
		a.logf("host agent: could not save the rotated token: %v", err)
		a.sendEphemeral(TypeError, ErrorPayload{Code: "rotate_failed", Message: "could not save the new token", Type: TypeAuthRotate}, env.ID)
		return
	}
	a.mu.Lock()
	a.creds = &c
	a.mu.Unlock()
	a.logf("host agent: host token rotated")
	a.sendReliable(TypeAuthRotated, struct{}{}, env.ID)
}

func rejected(code, msg string) ResultPayload {
	return ResultPayload{Status: StatusRejected, Error: &ResultError{Code: code, Message: msg}}
}

func failed(code, msg, output string) ResultPayload {
	return ResultPayload{Status: StatusFailed, Error: &ResultError{Code: code, Message: msg}, Output: output}
}

func okResult(output string) ResultPayload {
	return ResultPayload{Status: StatusOK, Output: strings.TrimSpace(output)}
}
