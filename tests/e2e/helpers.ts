import { execSync } from 'child_process';
import { existsSync, readFileSync } from 'fs';
import { dirname, join, resolve } from 'path';
import type { Page } from '@playwright/test';

/** Base URL of the server under test. Override with E2E_BASE_URL. */
export const BASE = process.env.E2E_BASE_URL ?? 'http://localhost:8091';

// Cache sessions to avoid hitting rate limits.
const sessionCache = new Map<string, string>();

/** Walk up from this file to the repository root (the directory with go.mod). */
function repoRoot(): string {
	let dir = __dirname;
	for (;;) {
		if (existsSync(join(dir, 'go.mod'))) return dir;
		const parent = dirname(dir);
		if (parent === dir) {
			throw new Error(`Could not locate the repository root above ${__dirname}`);
		}
		dir = parent;
	}
}

/**
 * Read the most recent server logs.
 *
 * Two setups are supported, so the suite runs the same way whether the server
 * came from `scripts/e2e.sh` or from docker compose:
 *
 *   - E2E_SERVER_LOG=/path/to/log — read that file directly.
 *   - otherwise — `docker compose logs`, run from the repository root.
 */
function readServerLogs(): string {
	const logFile = process.env.E2E_SERVER_LOG;
	if (logFile) {
		const path = resolve(logFile);
		if (!existsSync(path)) {
			throw new Error(`E2E_SERVER_LOG points at a file that does not exist: ${path}`);
		}
		return readFileSync(path, 'utf-8');
	}

	const service = process.env.E2E_COMPOSE_SERVICE ?? 'openrsvp';
	try {
		return execSync(`docker compose logs --tail=200 ${service} 2>&1`, {
			cwd: repoRoot(),
			encoding: 'utf-8'
		});
	} catch (err) {
		throw new Error(
			`Could not read server logs. Either set E2E_SERVER_LOG to the server's log ` +
				`file, or make sure "docker compose logs ${service}" works from the repo ` +
				`root. Run the suite with "make e2e" to have this handled for you. ` +
				`(${(err as Error).message})`
		);
	}
}

/**
 * Request a magic link and pull the token back out of the server log.
 *
 * The server logs the token only when ENV=development (see
 * internal/auth/service.go), which is what both supported setups use.
 */
export async function getAuthToken(email: string): Promise<string> {
	const res = await fetch(`${BASE}/api/v1/auth/magic-link`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ email })
	});
	if (!res.ok) throw new Error(`Magic link request failed: ${res.status}`);

	// Logs are written asynchronously, so poll briefly rather than guessing a
	// single sleep long enough for every machine.
	const deadline = Date.now() + 5000;
	for (;;) {
		const lines = readServerLogs().split('\n');
		for (const line of lines.reverse()) {
			if (line.includes('magic link generated') && line.includes(email)) {
				const tokenMatch = line.match(/token=([a-f0-9]{64})/);
				if (tokenMatch) return tokenMatch[1];
			}
		}
		if (Date.now() > deadline) break;
		await new Promise((r) => setTimeout(r, 250));
	}

	throw new Error(
		`Could not find a magic link token for ${email} in the server logs. ` +
			`The server must run with ENV=development for the token to be logged.`
	);
}

/** Verify a magic link token and return the session token. */
export async function verifyMagicLink(token: string): Promise<string> {
	const res = await fetch(`${BASE}/api/v1/auth/verify`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ token })
	});
	if (!res.ok) throw new Error(`Verify failed: ${res.status}`);
	const data = await res.json();
	return data.token;
}

/** Get or create a cached session for the given email. */
export async function getOrCreateSession(email: string): Promise<string> {
	if (sessionCache.has(email)) return sessionCache.get(email)!;
	const magicToken = await getAuthToken(email);
	const sessionToken = await verifyMagicLink(magicToken);
	sessionCache.set(email, sessionToken);
	return sessionToken;
}

/** Set an existing session token in the browser context. */
export async function setSessionInBrowser(
	page: Page,
	sessionToken: string
): Promise<void> {
	// Set cookie so server-side auth works.
	await page.context().addCookies([
		{
			name: 'session',
			value: sessionToken,
			domain: 'localhost',
			path: '/',
			httpOnly: true,
			sameSite: 'Lax'
		}
	]);
	// Use addInitScript so localStorage is set BEFORE any page JS runs.
	// This ensures the SPA reads the token on first load.
	await page.addInitScript((token: string) => {
		localStorage.setItem('openrsvp_session', token);
	}, sessionToken);
}

/** Clear session from browser. */
export async function clearSession(page: Page): Promise<void> {
	await page.evaluate(() => localStorage.removeItem('openrsvp_session'));
	await page.context().clearCookies();
}

/**
 * Default description for test events. Long enough to overflow the guest
 * pages' collapsed description, so the "Show more" toggle is exercised.
 */
export const TEST_EVENT_DESCRIPTION = [
	'This is an automated e2e test event.',
	'It has a deliberately long description so that the guest-facing pages have to clamp it to the first few lines and offer a way to expand the rest.',
	'Please bring a side dish if you can, and let us know about any dietary restrictions ahead of time.',
	'Parking is on the street and the gate code is 4821.'
].join('\n\n');

/** Create an event via API using Bearer token (bypasses CSRF). */
export async function createEventViaAPI(
	sessionToken: string,
	overrides: Record<string, unknown> = {}
): Promise<Record<string, unknown>> {
	const tomorrow = new Date();
	tomorrow.setDate(tomorrow.getDate() + 1);
	const dateStr = tomorrow.toISOString().split('T')[0];

	const eventData = {
		title: 'E2E Test Event ' + Date.now(),
		eventDate: `${dateStr}T18:00:00`,
		timezone: 'America/New_York',
		location: 'Test Venue, 123 Main St',
		description: TEST_EVENT_DESCRIPTION,
		maxCapacity: 50,
		rsvpDeadline: `${dateStr}T18:00:00`,
		commentsEnabled: true,
		...overrides
	};

	const res = await fetch(`${BASE}/api/v1/events`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			Authorization: `Bearer ${sessionToken}`
		},
		body: JSON.stringify(eventData)
	});
	if (!res.ok) {
		const body = await res.text();
		throw new Error(`Create event failed: ${res.status} ${body}`);
	}
	const json = await res.json();
	const event = json.data || json;

	// Publish the event so it's publicly accessible.
	const pubRes = await fetch(`${BASE}/api/v1/events/${event.id}/publish`, {
		method: 'POST',
		headers: {
			Authorization: `Bearer ${sessionToken}`
		}
	});
	if (!pubRes.ok) {
		const body = await pubRes.text();
		throw new Error(`Publish event failed: ${pubRes.status} ${body}`);
	}
	const pubJson = await pubRes.json();
	return pubJson.data || pubJson;
}

/** Submit an RSVP via API and return the response. */
export async function submitRSVPViaAPI(
	shareToken: string,
	data: { name: string; email: string; rsvpStatus: string }
): Promise<Record<string, unknown>> {
	const res = await fetch(`${BASE}/api/v1/rsvp/public/${shareToken}`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(data)
	});
	if (!res.ok) {
		const body = await res.text();
		throw new Error(`RSVP submission failed: ${res.status} ${body}`);
	}
	const json = await res.json();
	return json.data || json;
}
