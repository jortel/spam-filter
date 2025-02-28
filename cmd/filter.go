package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
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

// Filter spam emails found in the INBOX using domains
// and accounts found in INBOX.spm.
type Filter struct {
	askUser bool
	//
	eventChan chan Event
	domains   map[string]Domain
	filtered  imap.UIDSet
}

// Run spam is detected by matching the account and/or domain
// to messages found in the INBOX.spam folder. Spam found in the
// INBOX is moved to the INBOX.spam folder.
func (r *Filter) Run() {
	r.domains = make(map[string]Domain)
	r.eventChan = make(chan Event, 4096)
	r.filtered = imap.UIDSet{}
	r.watch(SPAM)
	r.watch(INBOX)
	r.fetchSpam()
	r.filterInbox()
	r.processEvents()
}

// open returns a client with the specified mailbox selected.
func (r *Filter) open(mailbox string) (client *imapclient.Client, count uint32) {
	client, count = r.openWith(mailbox, nil)
	return
}

// open returns a client with the specified options and mailbox selected.
func (r *Filter) openWith(mailbox string, options *imapclient.Options) (client *imapclient.Client, count uint32) {
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
	mb, err := selectCmd.Wait()
	if err != nil {
		panic(err)
	}
	count = mb.NumMessages
	return
}

// filterInbox fetches the INBOX, identifies spam and moves
// them to the Filtered folder.
// Spam is identified using the domains-catalog.
func (r *Filter) filterInbox() {
	messages := r.fetchInbox()
	if len(messages) == 0 {
		return
	}
	client, _ := r.open(INBOX)
	defer func() {
		_ = client.Close()
	}()
	for i := range messages {
		m := messages[i]
		r.filtered.AddNum(m.UID)
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
		r.spamDetected(client, m)
	}
}

// fetchSpam fetches the INBOX.spam mailbox and build the spam-catalog.
func (r *Filter) fetchSpam() {
	mailbox := SPAM
	client, count := r.open(mailbox)
	defer func() {
		_ = client.Close()
	}()
	r.domains = make(map[string]Domain)
	begin := uint32(1)
	end := count
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
		count,
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
	r.printDomains()
}

// printDomains prints the spam-catalog.
func (r *Filter) printDomains() {
	keyList := []string{}
	for k := range r.domains {
		keyList = append(keyList, k)
	}
	sort.Strings(keyList)
	fmt.Printf("SPAM Domains(%d):\n", len(keyList))
	for _, k := range keyList {
		d := r.domains[k]
		fmt.Printf("  %s\n", d.string())
	}
}

// fetchInbox fetches the INBOX and returns a list of unseen messages.
func (r *Filter) fetchInbox() (messages []*imapclient.FetchMessageBuffer) {
	var err error
	mailbox := INBOX
	client, count := r.open(mailbox)
	defer func() {
		_ = client.Close()
	}()
	mark := time.Now()
	var numSet imap.NumSet
	if len(r.filtered) > 0 {
		searchQ := &imap.SearchCriteria{
			Not: []imap.SearchCriteria{
				{
					UID: []imap.UIDSet{r.filtered},
				},
			},
		}
		searchOpt := &imap.SearchOptions{
			ReturnAll: true,
		}
		searchCmd := client.UIDSearch(searchQ, searchOpt)
		matched, err := searchCmd.Wait()
		if err != nil {
			panic(err)
		}
		numSet = matched.All
		if numSet == nil {
			return
		}
	} else {
		seqSet := imap.SeqSet{}
		seqSet.AddRange(1, count)
		numSet = seqSet
	}
	fetchOpt := &imap.FetchOptions{Envelope: true, Flags: true, UID: true}
	fetchCmd := client.Fetch(numSet, fetchOpt)
	messages, err = fetchCmd.Collect()
	if err != nil {
		panic(err)
	}
	fmt.Printf(
		"\nfetch[INBOX]: count: %d, matched: %d, duration: %s\n\n",
		count,
		len(messages),
		time.Since(mark))
	return
}

// spamDetected handles a message identified as spam.
// The message is moved to the `Filter` folder.
func (r *Filter) spamDetected(client *imapclient.Client, m *imapclient.FetchMessageBuffer) {
	if !r.promptUser() {
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

// promptUser prompts the user to confirm handling of the spam.
func (r *Filter) promptUser() (confirmed bool) {
	if !r.askUser {
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

// watch opens IDLE connections to mailboxes.
// Message events are queued in the eventChan.
func (r *Filter) watch(mailbox string) (cmd *imapclient.IdleCommand) {
	options := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Expunge: func(seqNum uint32) {
				r.expunged(mailbox)
			},
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					r.added(mailbox, *data.NumMessages)
				}
			},
		},
	}
	var err error
	client, _ := r.openWith(mailbox, options)
	cmd, err = client.Idle()
	if err != nil {
		panic(err)
	}
	return
}

// expunged enqueues message expunged events.
func (r *Filter) expunged(mailbox string) {
	event := Event{
		mailbox: mailbox,
		action:  Expunged,
	}
	r.eventChan <- event
	fmt.Printf("> %s\n", event.string())
}

// added enqueues message added events.
func (r *Filter) added(mailbox string, count uint32) {
	event := Event{
		mailbox: mailbox,
		action:  Added,
	}
	r.eventChan <- event
	fmt.Printf("> %s\n", event.string())
}

// processEvents applies queued message events.
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

// Domain represents a domain identified as spam.
// It contains a map of collated accounts in the domain.
type Domain struct {
	name    string
	account map[string]int
	count   int
}

// add an account to the domain.
func (d *Domain) add(account string) {
	d.count++
	if d.account == nil {
		d.account = make(map[string]int)
	}
	d.account[account] = d.account[account] + 1
}

// match returns true when the FROM account matches the domain.
// Matches when:
// - The domain contains multiple accounts.
// - The domain contains the specified account.
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

// sting returns a representation of the domain.
func (d *Domain) string() (s string) {
	s = fmt.Sprintf("(%s) nAccount: %d", d.name, len(d.account))
	return
}

// Event is a mailbox event.
type Event struct {
	mailbox string
	action  string
}

// string returns a string representation.
func (r *Event) string() (s string) {
	return fmt.Sprintf("Event: [%s] action: %s", r.mailbox, r.action)
}
