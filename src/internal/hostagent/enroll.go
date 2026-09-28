package hostagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// EnrollRequest is the body of POST /api/fleet/enroll for a host (FLEET.md
// §18.1; protocol/host-v1/http/enroll.request.json).
type EnrollRequest struct {
	Kind       string `json:"kind"` // always "host"
	Code       string `json:"code,omitempty"`
	Key        string `json:"key,omitempty"`
	MachineID  string `json:"machine_id"`
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	CSMVersion string `json:"csm_version"`
	TenantID   string `json:"tenant_id"`
}

// EnrollResponse is the 201 body (protocol/host-v1/http/enroll.response.json).
type EnrollResponse struct {
	Success    bool   `json:"success"`
	HostID     string `json:"host_id"`
	TenantID   string `json:"tenant_id"`
	Name       string `json:"name,omitempty"`
	Token      string `json:"token"`
	WSURL      string `json:"ws_url,omitempty"`
	Reenrolled bool   `json:"reenrolled,omitempty"`
}

// EnrollOptions are the inputs of Enroll.
type EnrollOptions struct {
	PlatformURL string
	// CodeOrKey is a one-time code (RUE-…) or a fleet enrollment key (rfk_…).
	CodeOrKey   string
	MachineID   string
	Hostname    string
	OS          string
	CSMVersion  string
	InsecureDev bool
	CAFile      string
	// HTTPClient overrides the client (tests).
	HTTPClient *http.Client
}

// EnrollError is a refused enrollment, with the platform's code.
type EnrollError struct {
	Status  int
	Code    string
	Message string
}

func (e *EnrollError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	hint := ""
	switch e.Status {
	case http.StatusUnauthorized:
		hint = " (the code or key is wrong, used or expired: make a new one on the platform)"
	case http.StatusForbidden:
		hint = " (the key or this host was revoked, expired or locked)"
	case http.StatusConflict:
		hint = " (the key's host limit is reached)"
	case http.StatusTooManyRequests:
		hint = " (too many attempts; wait a minute)"
	}
	if e.Code != "" {
		return fmt.Sprintf("enrollment refused: %s [%s, HTTP %d]%s", Redact(msg), e.Code, e.Status, hint)
	}
	return fmt.Sprintf("enrollment refused: %s [HTTP %d]%s", Redact(msg), e.Status, hint)
}

// Enroll trades a code or fleet key for a host identity and returns the
// credentials to store. Nothing is written here.
func Enroll(ctx context.Context, o EnrollOptions) (*Credentials, error) {
	base, err := CheckURL(strings.TrimRight(strings.TrimSpace(o.PlatformURL), "/"), o.InsecureDev, "https", "http")
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(o.CodeOrKey)
	req := EnrollRequest{
		Kind: "host", MachineID: o.MachineID, Hostname: o.Hostname, OS: o.OS,
		CSMVersion: o.CSMVersion, TenantID: "default",
	}
	isKey := strings.HasPrefix(secret, "rfk_")
	switch {
	case isKey && !IsFleetKey(secret):
		return nil, errors.New("that does not look like a fleet enrollment key (rfk_<id>_<secret>)")
	case isKey:
		req.Key = secret
	case secret == "" || len(secret) > 64:
		return nil, errors.New("give the one-time code (RUE-XXXX-XXXX-XXXX-XXXX) or a fleet key (rfk_…)")
	default:
		req.Code = secret
	}
	if req.MachineID == "" || req.Hostname == "" {
		return nil, errors.New("machine id and hostname are required")
	}

	client := o.HTTPClient
	if client == nil {
		tlsCfg, err := TLSConfig(o.CAFile)
		if err != nil {
			return nil, err
		}
		client = &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg, Proxy: http.ProxyFromEnvironment},
			// A redirect could move the secret to another host or to http.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	endpoint := *base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + EnrollPath
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("User-Agent", "csm/"+o.CSMVersion)
	resp, err := client.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("enrollment request failed: %s", Redact(err.Error()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("reading the enrollment answer: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var e struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return nil, &EnrollError{Status: resp.StatusCode, Code: truncate(e.Code, 64), Message: truncate(e.Error, 300)}
	}
	var out EnrollResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("the platform's enrollment answer is not JSON (is %s an Auto Tournament platform?)", base.Host)
	}
	if !out.Success || out.HostID == "" || !hostTokenRe.MatchString(out.Token) {
		return nil, errors.New("the platform's enrollment answer has no host id or host token (does it support hosts yet?)")
	}
	if out.TenantID != "" && out.TenantID != "default" {
		return nil, fmt.Errorf("unexpected tenant %q", truncate(out.TenantID, 32))
	}
	wsURL := DefaultWSURL(base)
	if strings.TrimSpace(out.WSURL) != "" {
		if _, err := CheckURL(out.WSURL, o.InsecureDev, "wss", "ws"); err != nil {
			return nil, fmt.Errorf("the platform's ws_url: %w", err)
		}
		wsURL = strings.TrimSpace(out.WSURL)
	}
	c := &Credentials{
		PlatformURL: base.String(),
		WSURL:       wsURL,
		HostID:      out.HostID,
		TenantID:    "default",
		Token:       out.Token,
		MachineID:   o.MachineID,
		EnrolledAt:  nowRFC3339(),
		InsecureDev: o.InsecureDev,
		CAFile:      o.CAFile,
	}
	if isKey {
		c.FleetKey = secret
	}
	return c, c.Validate()
}

// TLSConfig is the client TLS setup: verification always on, the OS roots
// plus an optional extra CA bundle.
func TLSConfig(caFile string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(caFile) == "" {
		return cfg, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading the CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s has no PEM certificates", caFile)
	}
	cfg.RootCAs = pool
	return cfg, nil
}
