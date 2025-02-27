package main

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	INBOX = "INBOX"
	SPAM  = "INBOX.spam"
)

type Filter struct {
	client     *imapclient.Client
	domains    map[string]Domain
	promptUser bool
}

// Run spam is detected by matching the account and/or domain
// to messages found in the INBOX.spam folder. Spam found in the
// INBOX is moved to the INBOX.spam folder.
func (r *Filter) Run() {
	r.domains = make(map[string]Domain)
	r.fetchSpam()
	fmt.Println("SPAM CATALOG:")
	for _, domain := range r.domains {
		fmt.Printf("  %s\n", domain.string())
	}

	messages := r.fetchInbox()

	for i := range messages {
		m := messages[i]
		domain := "NONE"
		account := "NONE"
		if len(m.Envelope.Sender) == 0 {
			continue
		}
		domain = m.Envelope.Sender[0].Host
		account = m.Envelope.From[0].Mailbox
		subject := m.Envelope.Subject
		spamDomain := r.domains[domain]

		if !spamDomain.match(account) {
			continue
		}

		fmt.Printf(
			"FOUND: (%s@%s) '%s' matched: %s\n",
			account,
			domain,
			subject,
			spamDomain.string())

		r.spamDetected(m)
	}
}

func (r *Filter) spamDetected(m *imapclient.FetchMessageBuffer) {
	if !r.confirm() {
		return
	}
	fmt.Printf(
		"\nMoving: uid:%d\n",
		m.UID)
	seqSet := imap.SeqSet{}
	seqSet.AddNum(m.SeqNum)
	moveCmd := r.client.Move(seqSet, SPAM)
	md, err := moveCmd.Wait()
	if err != nil {
		panic(err)
	}
	fmt.Printf(
		"\nMoved: uid:%s\n",
		md.SourceUIDs.String())
}

func (r *Filter) confirm() (confirmed bool) {
	if !r.promptUser {
		confirmed = true
		return
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("MOVE:[y|n]: ")
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(ans)
	ans = strings.ToUpper(ans)
	switch ans {
	case "Q":
		os.Exit(0)
	case "Y", "":
		confirmed = true
	}
	return
}

func (r *Filter) fetchInbox() (messages []*imapclient.FetchMessageBuffer) {
	selectCmd := r.client.Select(INBOX, nil)
	box, err := selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	begin := uint32(1)
	end := box.NumMessages
	seqSet := imap.SeqSet{}
	seqSet.AddRange(begin, end)
	options := &imap.FetchOptions{Envelope: true, Flags: true, UID: true}
	fetchCmd := r.client.Fetch(seqSet, options)
	mark := time.Now()
	messages, err = fetchCmd.Collect()
	if err != nil {
		panic(err)
	}
	slices.Reverse(messages)
	fmt.Printf(
		"\nfetch[INBOX]: count: %d, duration: %s\n\n",
		box.NumMessages,
		time.Since(mark))
	return
}

func (r *Filter) fetchSpam() {
	selectCmd := r.client.Select(SPAM, nil)
	box, err := selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	begin := uint32(1)
	end := box.NumMessages
	seqSet := imap.SeqSet{}
	seqSet.AddRange(begin, end)
	options := &imap.FetchOptions{Envelope: true, Flags: true}
	fetchCmd := r.client.Fetch(seqSet, options)
	mark := time.Now()
	messages, err := fetchCmd.Collect()
	if err != nil {
		panic(err)
	}

	fmt.Printf(
		"\nfetch[SPAM]: count: %d, duration: %s\n\n",
		box.NumMessages,
		time.Since(mark))

	for i := range messages {
		m := messages[i]
		host := "NONE"
		account := "NONE"
		if len(m.Envelope.Sender) > 0 {
			account = m.Envelope.From[0].Mailbox
			host = m.Envelope.Sender[0].Host
		}
		if inWhiteList(host) {
			continue
		}
		spam := r.domains[host]
		spam.name = host
		spam.add(account)
		r.domains[host] = spam
	}
}

func (r *Filter) watch() {
}

type Domain struct {
	name    string
	account map[string]int
	count   int
}

func (d *Domain) add(account string) {
	d.count++
	if d.account == nil {
		d.account = make(map[string]int)
	}
	d.account[account] = d.account[account] + 1
}

func (d *Domain) match(account string) (matched bool) {
	switch d.count {
	case 0:
		break
	case 1:
		_, matched = d.account[account]
	default:
		matched = true
	}
	return
}

func (d *Domain) string() (s string) {
	s = fmt.Sprintf("(%s) nAccount: %d", d.name, len(d.account))
	return
}
