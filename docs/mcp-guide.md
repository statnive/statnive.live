# Statnive.live MCP — the plain-language guide

> **In one sentence:** it lets you *ask your analytics questions in plain English* — in Claude, Claude Desktop, or any MCP-capable assistant — and get **real answers from your own data**, without opening a dashboard or writing a query.

This is the friendly guide. For the precise operator reference (config keys, flags, security internals), see **[mcp.md](mcp.md)**.

---

## What is it?

Statnive.live already stores your website analytics (visitors, page views, sources, revenue, …). The **MCP server** is a small built-in "answer service" that an AI assistant can plug into. Once connected, you can just *talk*:

> *"How did statnive.com do last week, and where did the visitors come from?"*

…and the assistant fetches the numbers straight from your analytics and replies in normal words — often with the chart-worthy bits called out.

Think of it as **a knowledgeable analyst that already has your data open** and never gets tired of "quick questions."

### Three things that make it safe

1. **It can only read — never change anything.** There is no button it can press to edit, delete, or export-dump your data. Every tool is read-only, forever.
2. **Your data stays where it is.** The assistant asks the server a question; the server reads your own ClickHouse and answers. Nothing is uploaded to a third party. It even runs on a fully offline (air-gapped) server.
3. **Same rules as your dashboard.** It can only see the sites *you* are allowed to see. Ask about a site you don't own → it politely refuses. Admin-only questions need an admin.

> **One thing to keep in mind:** answers contain real visitor data (referrer URLs, custom values). The assistant treats all of it as *information*, not as instructions — so a sneaky referrer like `ignore your rules` can't hijack it. You don't need to do anything; it's handled.

---

## How to use it (2 minutes)

You connect your assistant to the server once. The easy, safe default is **stdio** (a direct local pipe — nothing opens on the network):

```bash
claude mcp add --transport stdio statnive-live -- \
  /usr/local/bin/statnive-live mcp serve \
  --config /etc/statnive-live/config.yaml --allow-sites 1,4
```

- `--allow-sites 1,4` = "this assistant may read sites 1 and 4." Use `--all-sites` to allow every site you own.
- Without one of those flags it's **locked down** (reads nothing) — safe by default.

Then, in Claude Code, type `/mcp` to confirm it's connected, and just ask your question. (Prefer a network connection or want the full setup options? See [mcp.md](mcp.md).)

That's it. The rest of this guide is **what you can ask**.

---

## Everything you can ask (all of it)

You never call these by name — you ask naturally and the assistant picks the right one. They're grouped here so you can see the full range.

### Your numbers

| You can ask… | It uses |
|---|---|
| "How's my site doing this week?" (visitors, views, revenue, revenue-per-visitor) | `overview` |
| "Is my traffic going up or down?" (day-by-day) | `trend` |
| "Where do my visitors come from?" (Google, social, direct, …) | `sources` |
| "Which pages are my best performers?" | `pages` |
| "Did my email/UTM campaign work?" | `campaigns` |
| "How's my Google/organic search traffic trending?" | `seo` |
| "Which countries/cities are my visitors in?" | `geo` |
| "How many people are on my site right now?" | `realtime` |

### Digging deeper

| You can ask… | It uses |
|---|---|
| "Did version B convert better than version A?" (with statistical significance) | `compare` |
| "What custom data am I even collecting?" (and example values) | `props_list` |
| "What conversion goals do I have set up?" | `goals_list` |

### Getting your bearings

| You can ask… | It uses |
|---|---|
| "Which sites can I look at?" | `list_sites` |
| "What's my access level / can I see site X?" | `my_access` |
| "What version of Statnive is this, and what data sources does it credit?" | `about` |

### Admin / ops (admins only)

| You can ask… | It uses |
|---|---|
| "Is the analytics backend healthy right now?" | `system_health` |
| "Which of my sites enforce GPC/Do-Not-Track? What's site X's consent mode?" | `site_config` |
| "How many custom event types is site X using — am I over the privacy cap?" | `event_audit` |

*(Two more — "device/browser breakdown" and "conversion funnels" — are wired up and will start answering as soon as those data tables ship. Until then the assistant will tell you they're "not yet available" rather than guess.)*

Every answer is bounded and tidy (top results, sensible limits) so it never floods the chat.

---

## Creative, practical examples

Real things people actually do. Each shows what *you* say and what happens behind the scenes.

### 1. The Monday-morning 30-second review
> **You:** "Give me last week vs the week before for statnive.com — visitors, revenue, and the top 3 sources. Anything jump out?"

The assistant pulls `overview` for both weeks, grabs `sources`, compares them, and replies like: *"Visitors up 12% (1,240 → 1,389), revenue flat. Organic Search drove the growth (+30%); Direct slipped. Worth a look: a new referrer 'newsletter.beehiiv.com' appeared with strong revenue-per-visitor."*

### 2. Find the money (not just the traffic)
> **You:** "Which traffic source makes me the most money *per visitor*, not just the most visitors?"

Statnive is built around **revenue-per-visitor (RPV)** exactly for this. The assistant reads `sources`, sorts by RPV, and surfaces the source that's quietly out-earning the high-volume ones — the classic "200 visitors at $500 beats 10,000 at $50" insight.

### 3. Did the campaign pay off?
> **You:** "I ran a spring-sale UTM campaign. Did it convert, and did the 'hero-A' vs 'hero-B' landing variant matter?"

Two steps, automatically: `campaigns` to see the campaign's visitors/revenue, then `compare` with `dimension = session:landing_variant` and your purchase goal — which returns conversion rates *and* whether the difference is statistically real (it won't over-claim on a tiny sample).

### 4. "What am I even tracking?" (discovery)
> **You:** "What custom properties and goals do I have on site 1? Then compare conversions across my plan tiers."

The assistant runs `props_list` (finds e.g. `plan`, `ab_variant`) and `goals_list` (finds `Purchase`), then uses `compare` with `dimension = user:plan` and `goal = purchase` — turning "I forgot what I set up" into a real cohort comparison in one breath.

### 5. Privacy compliance sweep (admin)
> **You:** "Across all my sites, which ones enforce GPC, what consent mode is each in, and is any site over the consent-free event cap?"

The assistant lists your sites (`list_sites`), checks each one's `site_config` (GPC/DNT/consent mode/jurisdiction), and runs `event_audit` per site to flag any that exceed the CNIL 3-event ceiling — a compliance check that would take ages by hand.

### 6. "Is something broken?"
> **You:** "Traffic looks weird today — is the backend OK and how many people are on right now?"

`system_health` (is ClickHouse up?) + `realtime` (current active visitors) give you an instant "all good" or "here's the problem" — without SSHing anywhere.

### 7. Hand it a question you'd normally avoid
> **You:** "Make me a 5-bullet exec summary of statnive.com for the last 30 days a non-technical founder would understand."

Because the assistant can freely combine `overview`, `trend`, `sources`, `pages`, and `geo`, it writes the summary for you — the data is exact, the wording is human.

---

## Tips

- **Refer to a site any way you like** — its name (`statnive.com`), its short slug, or its number. The assistant figures it out.
- **Time ranges are flexible** — "today", "last 7 days", "last 30 days", "last quarter", or exact dates. Times use each site's own timezone.
- **Ask follow-ups** — it keeps context, so "now break that down by country" just works.
- **It won't invent numbers** — if data isn't there, it says so. Every figure traces back to your database.

## If you can't / don't want to use it

Everything here is also available the normal way: the Statnive **dashboard** and its API show the exact same numbers. The MCP adds *no new data* — it's just a faster, conversational door to what you already have. (A built-in check, `make mcp-parity`, guarantees the MCP can answer anything the dashboard can.)

---

*Operators & contributors:* the precise reference (transports, config, auth, security model, troubleshooting) lives in **[mcp.md](mcp.md)**.
