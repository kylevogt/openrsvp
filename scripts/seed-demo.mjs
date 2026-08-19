// seed-demo.mjs — Fill a running dev server with events you can look at.
//
// Creates one event per guest-list visibility setting, each with a spread of
// RSVPs (plus-ones, no plus-ones, declined), then prints the URLs as JSON.
// Zero dependencies: plain node, using the same public HTTP API a browser uses.
//
// Usage:
//   ./scripts/dev-server.sh --seed          # start a server and seed it
//   node scripts/seed-demo.mjs              # seed a server already running
//   node scripts/seed-demo.mjs --big        # ...plus a 55-guest event (slow)
//
// Environment:
//   DEMO_BASE_URL     server to seed (default http://localhost:8099)
//   DEMO_SERVER_LOG   server log to read magic-link tokens from
//                     (default $DEV_DIR/server.log, then .dev/server.log)
//   DEMO_HOST_EMAIL   organizer to create the events as
//
// Logging in requires ENV=development on the server: that is the only mode that
// writes magic-link tokens to the log, and there is no mail provider locally.

import { existsSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const BASE = process.env.DEMO_BASE_URL ?? 'http://localhost:8099';
const HOST_EMAIL = process.env.DEMO_HOST_EMAIL ?? 'host@example.com';
const SERVER_LOG =
	process.env.DEMO_SERVER_LOG ??
	join(process.env.DEV_DIR ?? join(ROOT, '.dev'), 'server.log');

const BIG = process.argv.includes('--big');

async function api(path, { method = 'GET', token, body, retryOn429 = false } = {}) {
	for (let attempt = 0; ; attempt++) {
		const res = await fetch(`${BASE}${path}`, {
			method,
			headers: {
				...(body ? { 'Content-Type': 'application/json' } : {}),
				// A Bearer token authenticates without a CSRF token, unlike a cookie.
				...(token ? { Authorization: `Bearer ${token}` } : {})
			},
			...(body ? { body: JSON.stringify(body) } : {})
		});

		// The public RSVP endpoint allows 30 requests/minute per IP
		// (internal/server/server.go, RSVPRateLimit). Wait the window out rather
		// than trying to predict it — the server's counter also includes requests
		// from earlier seed runs, so a client-side tally guesses wrong.
		if (res.status === 429 && retryOn429 && attempt < 5) {
			const retryAfter = await res.json().then((b) => b.retryAfter ?? 60, () => 60);
			process.stderr.write(`    ...rate limited, waiting ${retryAfter}s\n`);
			await new Promise((r) => setTimeout(r, (retryAfter + 2) * 1000));
			continue;
		}
		if (!res.ok) {
			throw new Error(`${method} ${path} -> ${res.status} ${await res.text()}`);
		}
		const json = await res.json();
		return json.data ?? json;
	}
}

/** Log in by requesting a magic link and reading the token back out of the log. */
async function login(email) {
	if (!existsSync(SERVER_LOG)) {
		throw new Error(
			`Server log not found at ${SERVER_LOG}. Start the server with ` +
				`./scripts/dev-server.sh, or point DEMO_SERVER_LOG at its log file.`
		);
	}
	await api('/api/v1/auth/magic-link', { method: 'POST', body: { email } });

	// The log is written asynchronously, so poll instead of guessing one sleep
	// that is long enough on every machine.
	const deadline = Date.now() + 5000;
	for (;;) {
		for (const line of readFileSync(SERVER_LOG, 'utf-8').split('\n').reverse()) {
			if (line.includes('magic link generated') && line.includes(email)) {
				const m = line.match(/token=([a-f0-9]{64})/);
				if (m) {
					const { token } = await api('/api/v1/auth/verify', {
						method: 'POST',
						body: { token: m[1] }
					});
					return token;
				}
			}
		}
		if (Date.now() > deadline) break;
		await new Promise((r) => setTimeout(r, 250));
	}
	throw new Error(
		`No magic-link token for ${email} in ${SERVER_LOG}. The server must run ` +
			`with ENV=development for tokens to be logged.`
	);
}

/** Create an event and publish it, so it is reachable at /i/{shareToken}. */
async function createEvent(token, overrides) {
	const date = new Date();
	date.setDate(date.getDate() + 14);
	const day = date.toISOString().split('T')[0];

	const event = await api('/api/v1/events', {
		method: 'POST',
		token,
		body: {
			eventDate: `${day}T18:00:00`,
			timezone: 'America/New_York',
			location: 'The Rooftop, 123 Main St',
			description:
				'Come celebrate with us! Bring whoever you like — just tell us how many.',
			commentsEnabled: true,
			...overrides
		}
	});
	return api(`/api/v1/events/${event.id}/publish`, { method: 'POST', token });
}

async function rsvp(shareToken, guest) {
	return api(`/api/v1/rsvp/public/${shareToken}`, {
		method: 'POST',
		retryOn429: true,
		body: { rsvpStatus: 'attending', ...guest }
	});
}

// A mix of plus-one counts, so guest-list pills show both "+n" and bare names.
const GUESTS = [
	{ name: 'Alice Nguyen', email: 'alice@example.com', plusOnes: 2 },
	{ name: 'Bob Martinez', email: 'bob@example.com', plusOnes: 1 },
	{ name: 'Carla Reyes', email: 'carla@example.com', plusOnes: 0 },
	{ name: 'Devon Park', email: 'devon@example.com', plusOnes: 3 },
	{ name: 'Erin Walsh', email: 'erin@example.com', plusOnes: 0 }
];

const token = await login(HOST_EMAIL);

// One event per visibility setting: the two toggles are independent, and the
// public payload differs in all three combinations.
const events = {
	headcountAndGuestList: await createEvent(token, {
		title: 'Rooftop Dinner (headcount + guest list)',
		showHeadcount: true,
		showGuestList: true
	}),
	guestListOnly: await createEvent(token, {
		title: 'Surprise Party (guest list, no headcount)',
		showHeadcount: false,
		showGuestList: true
	}),
	headcountOnly: await createEvent(token, {
		title: 'Book Club (headcount, no guest list)',
		showHeadcount: true,
		showGuestList: false
	})
};

const rsvpTokens = {};
for (const [key, event] of Object.entries(events)) {
	for (const guest of GUESTS) {
		const attendee = await rsvp(event.shareToken, guest);
		if (key === 'headcountAndGuestList') rsvpTokens[guest.name] = attendee.rsvpToken;
	}
	// A declined guest, who should never appear in the list or the headcount.
	await rsvp(event.shareToken, {
		name: 'Frank Ito',
		email: 'frank@example.com',
		rsvpStatus: 'declined'
	});
}

const out = {
	hostEmail: HOST_EMAIL,
	invitePages: Object.fromEntries(
		Object.entries(events).map(([k, e]) => [k, `${BASE}/i/${e.shareToken}`])
	),
	guestRsvpPage: `${BASE}/r/${rsvpTokens['Alice Nguyen']}`,
	publicApi: `${BASE}/api/v1/rsvp/public/${events.headcountAndGuestList.shareToken}`
};

// 55 guests: enough to push past the guest list's 50-pill cutoff and show the
// "+N more" toggle. Off by default because waiting out the RSVP rate limit
// makes it take a couple of minutes.
if (BIG) {
	process.stderr.write('  seeding 55-guest event...\n');
	const big = await createEvent(token, {
		title: 'Big Reunion (55 guests)',
		showHeadcount: true,
		showGuestList: true
	});
	const first = ['Ana', 'Ben', 'Cleo', 'Dan', 'Eve', 'Finn', 'Gus', 'Hana', 'Ivan', 'Jo'];
	const last = ['Adams', 'Baker', 'Chen', 'Diaz', 'Evans', 'Fox'];
	for (let i = 0; i < 55; i++) {
		await rsvp(big.shareToken, {
			name: `${first[i % first.length]} ${last[Math.floor(i / first.length) % last.length]} ${String(i).padStart(2, '0')}`,
			email: `guest${i}@example.com`,
			plusOnes: i % 4 === 0 ? (i % 3) + 1 : 0
		});
	}
	out.invitePages.bigGuestList = `${BASE}/i/${big.shareToken}`;
}

console.log(JSON.stringify(out, null, 2));
