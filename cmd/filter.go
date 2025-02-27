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
	INBOX    = "INBOX"
	SPAM     = "INBOX.spam"
	FILTERED = "INBOX.Filtered"
	Added    = "Added"
	Expunged = "Expunged"
)

type Filter struct {
	promptUser bool
	//
	domains   map[string]Domain
	eventChan chan Event
}

// Run spam is detected by matching the account and/or domain
// to messages found in the INBOX.spam folder. Spam found in the
// INBOX is moved to the INBOX.spam folder.
func (r *Filter) Run() {
	r.domains = make(map[string]Domain)
	r.eventChan = make(chan Event, 100)
	r.watch(SPAM)
	r.watch(INBOX)
	r.fetchSpam()
	r.filterInbox()
	r.processEvents()
}

func (r *Filter) open(mailbox string) (client *imapclient.Client) {
	client = r.openWith(mailbox, nil)
	return
}

func (r *Filter) openWith(mailbox string, options *imapclient.Options) (client *imapclient.Client) {
	var err error
	client, err = imapclient.DialTLS(Host, options)
	if err != nil {
		panic(err)
	}
	cmd := client.Login(User, Password)
	err = cmd.Wait()
	if err != nil {
		panic(err)
	}
	selectCmd := client.Select(mailbox, nil)
	_, err = selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	return
}

func (r *Filter) filterInbox() {
	messages := r.fetchInbox()
	if len(messages) == 0 {
		return
	}
	inboxClient := r.open(INBOX)
	defer func() {
		_ = inboxClient.Close()
	}()
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

		r.spamDetected(inboxClient, m)
	}
}

func (r *Filter) fetchSpam() {
	client := r.open(SPAM)
	defer func() {
		_ = client.Close()
	}()
	r.domains = make(map[string]Domain)
	selectCmd := client.Select(SPAM, nil)
	box, err := selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	begin := uint32(1)
	end := box.NumMessages
	seqSet := imap.SeqSet{}
	seqSet.AddRange(begin, end)
	options := &imap.FetchOptions{Envelope: true, Flags: true, UID: true}
	fetchCmd := client.Fetch(seqSet, options)
	mark := time.Now()
	messages, err := fetchCmd.Collect()
	if err != nil {
		panic(err)
	}
	fmt.Printf(
		"\nfetch[SPAM]: count: %d, duration: %s\n\n",
		box.NumMessages,
		time.Since(mark))
	fmt.Println("SPAM CATALOG:")
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
		fmt.Printf("  %s\n", spam.string())
	}
}

func (r *Filter) fetchInbox() (messages []*imapclient.FetchMessageBuffer) {
	client := r.open(INBOX)
	defer func() {
		_ = client.Close()
	}()
	selectCmd := client.Select(INBOX, nil)
	box, err := selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	begin := uint32(1)
	end := box.NumMessages
	seqSet := imap.SeqSet{}
	seqSet.AddRange(begin, end)
	options := &imap.FetchOptions{Envelope: true, Flags: true, UID: true}
	fetchCmd := client.Fetch(seqSet, options)
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

func (r *Filter) spamDetected(client *imapclient.Client, m *imapclient.FetchMessageBuffer) {
	if !r.confirm() {
		return
	}
	fmt.Printf("\nMoving: uid:%d\n", m.UID)
	uidSet := imap.UIDSet{}
	uidSet.AddNum(m.UID)
	moveCmd := client.Move(uidSet, FILTERED)
	md, err := moveCmd.Wait()
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nMoved: uid:%s\n", md.SourceUIDs.String())
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

func (r *Filter) watch(mailbox string) (cmd *imapclient.IdleCommand) {
	options := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Expunge: func(seqNum uint32) {
				r.expunged(mailbox)
			},
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					r.added(mailbox)
				}
			},
		},
	}
	var err error
	client := r.openWith(mailbox, options)
	cmd, err = client.Idle()
	if err != nil {
		panic(err)
	}
	return
}

func (r *Filter) expunged(mailbox string) {
	event := Event{
		mailbox: mailbox,
		action:  Expunged,
	}
	r.eventChan <- event
	fmt.Printf("> %s\n", event.string())
}

func (r *Filter) added(mailbox string) {
	event := Event{
		mailbox: mailbox,
		action:  Added,
	}
	r.eventChan <- event
	fmt.Printf("> %s\n", event.string())
}

func (r *Filter) processEvents() {
	for event := range r.eventChan {
		fmt.Printf("< %s\n", event.string())
		switch event.mailbox {
		case SPAM:
			r.fetchSpam()
		case INBOX:
			switch event.action {
			case Added:
				r.filterInbox()
			}
		}
	}
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

type Event struct {
	mailbox string
	action  string
}

func (r *Event) string() (s string) {
	return fmt.Sprintf("Event: [%s] action: %s", r.mailbox, r.action)
}
