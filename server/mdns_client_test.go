package weavedns

import (
	"github.com/miekg/dns"
	"log"
	"net"
	"testing"
	"time"
)

var (
	success_test_name = "test1.weave."
	fail_test_name    = "test2.weave."
	test_addr         = net.ParseIP("9.8.7.6")
)

func minimalServer(w dns.ResponseWriter, req *dns.Msg) {
	//log.Println("minimalServer received:", req)
	if len(req.Answer) > 0 {
		return // Only interested in questions.
	}
	if len(req.Question) != 1 {
		return // We only handle single-question messages
	}
	if req.Question[0].Name != success_test_name {
		return // This is not the DNS record you are looking for
	}

	m := new(dns.Msg)
	m.SetReply(req)

	hdr := dns.RR_Header{Name: m.Question[0].Name, Rrtype: dns.TypeA,
		Class: dns.ClassINET, Ttl: 3600}
	a := &dns.A{hdr, test_addr}
	m.Answer = append(m.Answer, a)

	buf, err := m.Pack()
	if err != nil {
		log.Fatal(err)
	}
	if buf == nil {
		log.Fatal("Nil buffer")
	}
	//log.Println("minimalServer sending:", buf)
	// This is a bit of a kludge - per the RFC we should send responses from 5353, but that doesn't seem to work
	sendconn, err := net.DialUDP("udp", nil, ipv4Addr)
	if err != nil {
		log.Fatal(err)
	}
	_, err = sendconn.Write(buf)
	sendconn.Close()
	if err != nil {
		log.Fatal(err)
	}
}

func RunLocalMulticastServer() (*dns.Server, error) {
	multicast, err := net.ListenMulticastUDP("udp", nil, ipv4Addr)
	if err != nil {
		return nil, err
	}
	server := &dns.Server{Listener: nil, PacketConn: multicast, Handler: dns.HandlerFunc(minimalServer)}
	go server.ActivateAndServe()
	return server, nil
}

func setup(t *testing.T) (*MDNSClient, *dns.Server, error) {
	mdnsClient, err := NewMDNSClient()
	if err != nil {
		t.Fatal(err)
	}
	err = mdnsClient.Start(nil)
	if err != nil {
		t.Fatal(err)
	}

	server, err := RunLocalMulticastServer()
	if err != nil {
		t.Fatalf("Unable to run test server: %s", err)
	}
	return mdnsClient, server, err
}

type testContext struct {
	received_addr  net.IP
	received_count int
}

func (c *testContext) checkResponse(t *testing.T, resp *ResponseA) {
	if resp.err != nil {
		t.Fatal(resp.err)
	}
	log.Printf("Got address response %s addr %s", resp.Name, resp.Addr)
	c.received_addr = resp.Addr
	c.received_count++
}

func TestSimpleQuery(t *testing.T) {
	log.Println("TestSimpleQuery starting")
	mdnsClient, server, _ := setup(t)
	defer mdnsClient.Shutdown()
	defer server.Shutdown()

	var context testContext
	channel := make(chan *ResponseA, 4)

	// First, a test we expect to succeed
	mdnsClient.SendQuery(success_test_name, dns.TypeA, channel)
	for resp := range channel {
		context.checkResponse(t, resp)
	}

	if !context.received_addr.Equal(test_addr) {
		t.Log("Unexpected result for", success_test_name, context.received_addr)
		t.Fail()
	}

	// Now, a test we expect to time out with no responses
	context.received_count = 0
	channel2 := make(chan *ResponseA, 4)
	mdnsClient.SendQuery("test2.weave.", dns.TypeA, channel2)
	for resp := range channel2 {
		context.checkResponse(t, resp)
	}

	if context.received_count > 0 {
		t.Log("Unexpected result for test2.weave", context.received_addr)
		t.Fail()
	}
}

func TestParallelQuery(t *testing.T) {
	log.Println("TestParallelQuery starting")
	mdnsClient, server, _ := setup(t)
	defer mdnsClient.Shutdown()
	defer server.Shutdown()

	var context1 testContext
	var context2 testContext
	channel1 := make(chan *ResponseA, 4)
	channel2 := make(chan *ResponseA, 4)

	go mdnsClient.SendQuery(success_test_name, dns.TypeA, channel1)
	go mdnsClient.SendQuery(success_test_name, dns.TypeA, channel2)
	timeout := time.After(2 * time.Second)
outerloop:
	for channel1 != nil || channel2 != nil {
		select {
		case resp, ok := <-channel1:
			if !ok {
				channel1 = nil
				continue
			}
			context1.checkResponse(t, resp)
		case resp, ok := <-channel2:
			if !ok {
				channel2 = nil
				continue
			}
			context2.checkResponse(t, resp)
		case <-timeout:
			break outerloop
		}
	}

	if !context1.received_addr.Equal(test_addr) || !context2.received_addr.Equal(test_addr) || context1.received_count != 1 || context2.received_count != 1 {
		t.Log("Unexpected result for", success_test_name, context1.received_addr, context2.received_addr, context1.received_count, context2.received_count)
		t.Fail()
	}
}
