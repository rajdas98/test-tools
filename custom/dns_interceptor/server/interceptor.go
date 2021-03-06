package server

import (
	"errors"
	"github.com/miekg/dns"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

// DNSInterceptor instance helps create a dns server that intercepts dns requests and injects chaos
type DNSInterceptor struct {
	client         dns.Client
	config         *dns.ClientConfig
	settings       *InterceptorSettings
	server         *dns.Server
	originalConfig string
	configPath     string
}

// NewDNSInterceptor creates a new instance of the DNSInterceptor and updates the resolv.conf to point to the interceptor
func NewDNSInterceptor(resolvConfPath string) (*DNSInterceptor, error) {
	conf, err := dns.ClientConfigFromFile(resolvConfPath)
	if err != nil {
		return nil, errors.New("failed to get resolv.conf : " + err.Error())
	}

	settings, err := getInterceptorSettings()
	if err != nil {
		return nil, err
	}

	original, err := updateResolvConf(resolvConfPath, nil)
	if err != nil {
		return nil, errors.New("failed to inject interceptor in resolv.conf : " + err.Error())
	}

	return &DNSInterceptor{
		client: dns.Client{
			ReadTimeout: 5 * time.Second,
		},
		config:         conf,
		settings:       settings,
		originalConfig: original,
		configPath:     resolvConfPath,
	}, nil
}

// Serve starts the interceptor server
func (d *DNSInterceptor) Serve(pattern string) {
	dns.HandleFunc(pattern, d.dnsHandler)
	d.server = &dns.Server{Addr: ":53", Net: "udp"}
	go func() {
		if err := d.server.ListenAndServe(); err != nil {
			d.Shutdown()
			log.WithError(err).Fatal("Failed to start dns interceptor")
		}
	}()
}

// Shutdown is responsible for clean up, it stops the dns interceptor and recovers the resolv.conf to original state
func (d *DNSInterceptor) Shutdown() {
	_, err := updateResolvConf(d.configPath, &d.originalConfig)
	if err != nil {
		log.WithError(err).Error("Failed to recover original resolv.conf")
	}
	if d.server != nil {
		err = d.server.Shutdown()
		if err != nil {
			log.WithError(err).Error("Failed to to shutdown interceptor")
		}
	}
}

// dnsHandler is responsible to handle the dns queries intercepted by dns interceptor
func (d *DNSInterceptor) dnsHandler(writer dns.ResponseWriter, msg *dns.Msg) {
	for _, q := range msg.Question {
		// in theory there can be multiple questions in a dns query but practically nameservers handle only 1 question
		if d.isChaosTarget(q.Name) {
			log.WithField("query", q.Name).Info("Intercepted target query")
			writer.WriteMsg(msg)
			return
		}
	}
	r, _, err := d.client.Exchange(msg, d.config.Servers[0]+":"+d.config.Port)
	if err != nil {
		log.WithError(err).WithField("server", d.config.Servers[0]+":"+d.config.Port).Error("Error while forwarding query to dns server")
		writer.WriteMsg(msg)
		return
	}
	writer.WriteMsg(r)
}

// isChaosTarget checks if the current query is chaos target depending on the InterceptorSettings
func (d *DNSInterceptor) isChaosTarget(query string) bool {
	if d.settings.TargetHostNames == nil {
		return true
	}
	for _, t := range d.settings.TargetHostNames {
		if d.settings.MatchType == Exact {
			if t == query || t == strings.TrimRight(query, ".") {
				return true
			}
		} else {
			if strings.Contains(query, t) {
				return true
			}
		}
	}
	return false
}
