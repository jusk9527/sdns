package failover

import (
	"context"
	"net"
	"strings"
	"sync"

	"github.com/miekg/dns"
	"github.com/semihalev/log"
	"github.com/semihalev/sdns/config"
	"github.com/semihalev/sdns/ctx"
	"github.com/semihalev/sdns/dnsutil"
	"github.com/semihalev/sdns/middleware"
)

// Failover type
type Failover struct {
	servers []string
}

// ResponseWriter implement of ctx.ResponseWriter
type ResponseWriter struct {
	ctx.ResponseWriter

	f *Failover
}

func init() {
	middleware.Register(name, func(cfg *config.Config) ctx.Handler {
		return New(cfg)
	})
}

// New return accesslist
func New(cfg *config.Config) *Failover {
	fallbackservers := []string{}
	for _, s := range cfg.FallbackServers {
		host, _, _ := net.SplitHostPort(s)

		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			fallbackservers = append(fallbackservers, s)
		} else if ip != nil && ip.To16() != nil {
			fallbackservers = append(fallbackservers, s)
		} else {
			log.Error("Fallback server is not correct. Check your config.", "server", s)
		}
	}

	return &Failover{servers: fallbackservers}
}

// Name return middleware name
func (f *Failover) Name() string { return name }

// ServeDNS implements the Handle interface.
func (f *Failover) ServeDNS(ctx context.Context, dc *ctx.Context) {
	w := dc.DNSWriter

	rw := AcquireWriter()
	rw.ResponseWriter = w
	rw.f = f

	dc.DNSWriter = rw

	dc.NextDNS(ctx)

	dc.DNSWriter = w
}

// WriteMsg implements the ctx.ResponseWriter interface
func (w *ResponseWriter) WriteMsg(m *dns.Msg) error {
	defer ReleaseWriter(w)

	if len(m.Question) == 0 || len(w.f.servers) == 0 {
		return w.ResponseWriter.WriteMsg(m)
	}

	if m.Rcode != dns.RcodeServerFailure || !m.RecursionDesired {
		return w.ResponseWriter.WriteMsg(m)
	}

	req := new(dns.Msg)
	req.SetQuestion(m.Question[0].Name, m.Question[0].Qtype)
	req.SetEdns0(dnsutil.DefaultMsgSize, true)
	req.RecursionDesired = true
	req.CheckingDisabled = m.CheckingDisabled

	for _, server := range w.f.servers {
		resp, err := dns.Exchange(req, server)
		if err != nil {
			log.Warn("Failover query failed", "query", formatQuestion(req.Question[0]), "error", err.Error())
			continue
		}

		resp.Id = m.Id

		return w.ResponseWriter.WriteMsg(resp)
	}

	return w.ResponseWriter.WriteMsg(m)
}

func formatQuestion(q dns.Question) string {
	return strings.ToLower(q.Name) + " " + dns.ClassToString[q.Qclass] + " " + dns.TypeToString[q.Qtype]
}

var writerPool sync.Pool

// AcquireWriter returns an empty msg from pool
func AcquireWriter() *ResponseWriter {
	v := writerPool.Get()
	if v == nil {
		return &ResponseWriter{}
	}
	return v.(*ResponseWriter)
}

// ReleaseWriter returns msg to pool
func ReleaseWriter(r *ResponseWriter) {
	writerPool.Put(r)
}

const name = "failover"
