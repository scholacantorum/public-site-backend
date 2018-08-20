package main

import (
	"fmt"
)

var products = map[string]productinfo{
	"subscription.2018-19": subscription{"2018-19", 7200},
	"ticket.2018-11-03": ticket{`"For the Love of Bach"`, "", "November 3", 2800,
		"November 3, 2018 at 7:30pm, at First Congregational Church of Palo Alto"},
	"ticket.2018-11-04": ticket{`"For the Love of Bach"`, "", "November 4", 2800,
		"November 4, 2018 at 3:00pm, at Los Altos United Methodist Church"},
	"ticket.2019-03-16": ticket{"Carmina Burana", "", "March 16", 2800,
		"March 16, 2019 at 7:30pm, at First Congregational Church of Palo Alto"},
	"ticket.2019-03-17": ticket{"Carmina Burana", "", "March 17", 2800,
		"March 17, 2019 at 3:00pm, at Los Altos United Methodist Church"},
}

type subscription struct {
	season string
	price  int64
}

func (s subscription) amount(qty int) int64 {
	return s.price * int64(qty)
}

func (s subscription) description(qty int) string {
	if qty == 1 {
		return fmt.Sprintf("%s Season Subscription", s.season)
	}
	return fmt.Sprintf("%s Season Subscriptions (%d at $%d each)", s.season, qty, s.price/100)
}

func (s subscription) thankyou(qty int) string {
	if qty == 1 {
		return fmt.Sprintf("Thank you for your purchase of a subscription to Schola Cantorum’s %s season.", s.season)
	}
	return fmt.Sprintf("Thank you for your purchase of %d subscriptions to Schola Cantorum’s %s season.", qty, s.season)
}

func (s subscription) message() string {
	return `
<p>Subscriptions include one ticket to each of the four Schola Cantorum concerts:<ul>
<li>“For the Love of Bach”, November 3 or 4</li>
<li>A John Rutter Christmas, December 16</li>
<li>Carmina Burana, March 16 or 17</li>
<li>Ein deutsches Requiem, May 24</li>
</ul><p>Your tickets will be mailed to the address you provided, at least two
weeks prior to the first concert.</p>`
}

type ticket struct {
	title string
	class string
	date  string
	price int64
	dtp   string
}

func (t ticket) amount(qty int) int64 {
	return t.price * int64(qty)
}

func (t ticket) description(qty int) (desc string) {
	if qty != 1 {
		desc = fmt.Sprintf("%d ", qty)
	}
	if t.class != "" {
		desc += t.class + " "
	}
	if qty == 1 {
		desc += "Ticket to "
	} else {
		desc += "Tickets to "
	}
	desc += t.title
	if t.date != "" {
		desc += ", " + t.date
	}
	return desc
}

func (t ticket) thankyou(qty int) (ty string) {
	ty = "Thank you for your purchase of "
	if qty == 1 {
		ty += "one ticket to "
	} else {
		ty += fmt.Sprintf("%d tickets to ", qty)
	}
	ty += t.title
	if t.date != "" {
		ty += fmt.Sprintf(" on %s", t.date)
	}
	ty += "."
	return ty
}

func (t ticket) message() string {
	return fmt.Sprintf(`
<p>This concert will be held on %s.
Tickets are held at will-call.  Please arrive 15–20 minutes early to allow time
to park and find your seats.</p>`, t.dtp)
}
