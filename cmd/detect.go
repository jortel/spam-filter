package main

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func detectSpam(client *imapclient.Client) {
	domains := make(map[string]int)
	spam := make(map[string]int)
	filter := make(map[string]int)
	accounts := make(map[string]map[string]int)
	count := uint32(0)

	// List mailboxes.
	fmt.Printf("Mailboxes:\n")
	ListCmd := client.List("", "*", nil)
	names, err := ListCmd.Collect()
	if err != nil {
		panic(err)
	}

	for _, name := range names {
		fmt.Printf(name.Mailbox + "\n")
	}

	// List emails.
	for _, name := range []string{"INBOX"} {
		selectCmd := client.Select(name, nil)
		box, err := selectCmd.Wait()
		if err != nil {
			panic(err)
		}
		begin := uint32(1)
		end := box.NumMessages
		seqSet := imap.SeqSet{}
		seqSet.AddRange(begin, end)
		options := &imap.FetchOptions{Envelope: true, Flags: true}
		fetchCmd := client.Fetch(seqSet, options)
		mark := time.Now()
		messages, err := fetchCmd.Collect()
		if err != nil {
			panic(err)
		}

		fmt.Printf("Fetched: %s\n", time.Since(mark))

		count += box.NumMessages

		fmt.Printf("mailbox: %s, count: %d\n\n", name, box.NumMessages)

		for i := range messages {
			m := messages[i]
			host := "NONE"
			account := "NONE"
			if len(m.Envelope.Sender) > 0 {
				host = m.Envelope.Sender[0].Host
				account = m.Envelope.Sender[0].Mailbox
			}
			// domain
			domains[host] = domains[host] + 1
			// accounts
			d := accounts[host]
			if d == nil {
				d = make(map[string]int)
			}
			d[account] = d[account] + 1
			accounts[host] = d
			// spam
			if name == "INBOX.spam" {
				spam[host] = spam[host] + 1
			}
		}
	}
	var hosts []string
	for host := range domains {
		hosts = append(hosts, host)
	}
	sort.SliceStable(
		hosts,
		func(i, j int) bool {
			return domains[hosts[i]] > domains[hosts[j]]
		})

	// domains

	for _, host := range hosts {
		n := domains[host]
		fmt.Printf("host (count=%.4d): %s\n", n, host)
	}

	fmt.Printf("\ncount: %d, unique domains: %d\n\n", count, len(domains))

	// blacklisted

	count = 0
	affected := 0
	fmt.Printf("\ndomain blacklisted:\n")
	for _, host := range hosts {
		if inBlackList(host) {
			count++
			n := domains[host]
			affected += n
			filter[host] = filter[host] + 1
			fmt.Printf("(count=%.4d): %s\n", n, host)
		}
	}

	fmt.Printf("count: %d, affected: %d\n\n", count, affected)

	// multiple dot.

	count = 0
	affected = 0
	fmt.Printf("\ndomain contains 3+ dot\n")
	for _, host := range hosts {
		if strings.Count(host, ".") > 2 {
			count++
			n := domains[host]
			affected += n
			filter[host] = filter[host] + 1
			fmt.Printf("(count=%.4d): %s\n", n, host)
		}
	}

	fmt.Printf("count: %d, affected: %d\n\n", count, affected)

	// health

	count = 0
	affected = 0
	fmt.Printf("\ndomain contains 'health'\n")
	for _, host := range hosts {
		if strings.Contains(host, "health") {
			n := domains[host]
			count++
			affected += n
			filter[host] = filter[host] + 1
			fmt.Printf("(count=%.4d): %s\n", n, host)
		}
	}

	fmt.Printf("count: %d, affected: %d\n\n", count, affected)

	// not .com

	count = 0
	affected = 0
	permitDot := []string{
		".com",
		".us",
		".gov",
		".mil",
		".org",
		".net",
		".edu",
	}
	fmt.Printf("\nnot %s\n", permitDot)
	for _, host := range hosts {
		dot := strings.ToLower(path.Ext(host))
		if slices.Contains(permitDot, dot) {
			continue
		}
		n := domains[host]
		count++
		affected += n
		filter[host] = filter[host] + 1
		fmt.Printf("(count=%.4d): %s\n", n, host)
	}

	fmt.Printf("count: %d, affected: %d\n\n", count, affected)

	//  filter?

	count = 0
	affected = 0
	fmt.Printf("\nplanned filter\n")
	for _, host := range hosts {
		n := filter[host]
		if n < 1 {
			continue
		}
		count++
		affected += n
		account := accounts[host]
		fmt.Printf("\n(count=%.4d): %s\n", n, host)
		for a, n := range account {
			if a != "" {
				fmt.Printf("    (count=%.4d) %s\n", n, a)
			}
		}
	}

	fmt.Printf("count: %d, affected: %d\n\n", count, affected)

	//  blacklist?

	affected = 0
	var blackList []string
	for _, host := range hosts {
		n := spam[host]
		if n < 1 {
			continue
		}
		affected += n
		if inWhiteList(host) {
			continue
		}
		blackList = append(blackList, host)
	}
	sort.Strings(blackList)
	fmt.Printf("\n(count=%d,affected=%d) blacklist:\n", len(blackList), affected)
	for _, host := range blackList {
		fmt.Printf("*@%s\n", host)
	}
}
