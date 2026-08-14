/**
 * Theme helpers.
 *
 * The initial theme is applied by an inline script in app.html (before first
 * paint). These helpers keep runtime toggles in sync with the same three
 * surfaces that script touches: the `data-theme` attribute, the persisted
 * preference, and the `theme-color` meta the mobile browser chrome reads.
 */

/** Page background per theme — must match `--color-neutral-50` in app.css. */
const THEME_COLORS = {
	light: '#FAFAF9',
	dark: '#0C0A09'
} as const;

export type Theme = 'light' | 'dark';

/** The theme currently applied to the document. */
export function currentTheme(): Theme {
	if (typeof document === 'undefined') return 'light';
	return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
}

/** Apply a theme to the document, optionally persisting it as the preference. */
export function applyTheme(theme: Theme, persist = true) {
	if (typeof document === 'undefined') return;

	if (theme === 'dark') {
		document.documentElement.setAttribute('data-theme', 'dark');
	} else {
		document.documentElement.removeAttribute('data-theme');
	}

	if (persist) {
		try {
			localStorage.setItem('theme', theme);
		} catch {
			// Private browsing / storage disabled — the theme still applies for this page.
		}
	}

	// app.html ships two media-scoped theme-color metas. Once the user (or a
	// stored preference) picks a theme explicitly, collapse them into a single
	// unconditional tag so the browser chrome follows the app, not the OS.
	const metas = document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]');
	metas.forEach((meta, i) => {
		if (i > 0) {
			meta.remove();
			return;
		}
		meta.removeAttribute('media');
		meta.setAttribute('content', THEME_COLORS[theme]);
	});
}

/** Flip between light and dark, returning the newly applied theme. */
export function toggleTheme(): Theme {
	const next: Theme = currentTheme() === 'dark' ? 'light' : 'dark';
	applyTheme(next);
	return next;
}
