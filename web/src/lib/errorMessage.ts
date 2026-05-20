import { HttpError } from '../api/admin';

// errorMessage translates an HttpError (or any thrown value) into a
// plain-language sentence the operator can act on. Pure / sync — the
// HttpError already carries the parsed body, so no async unwrap.
export function errorMessage(e: unknown, fallback: string): string {
  if (!(e instanceof HttpError)) {
    if (e instanceof Error && e.message) return `${fallback} ${e.message}`;
    return fallback;
  }

  const detail = e.serverMessage || 'no detail provided';

  if (e.status >= 500 && e.status <= 599) {
    return "Couldn't save your changes. The server is having trouble; try again, and if it keeps failing, contact your operator with the time and the site ID.";
  }

  switch (e.status) {
    case 400:
      return `We couldn't save your changes. The server flagged this: ${detail}. Check the highlighted field and try again.`;
    case 401:
      return 'Your session has expired. Sign in again to keep editing.';
    case 403:
      return "Your account doesn't have permission to change this. Ask an admin on your site to grant it.";
    case 404:
      return "We couldn't find this site any more. Refresh the page; if it's gone, ask an admin.";
    case 409:
      if (e.code === 'origin_already_registered') {
        return `${detail} is already registered to another site. Remove it there first or use a different subdomain.`;
      }
      if (e.code === 'slug_taken') return 'That slug is already used. Pick a different one.';
      return `That value is already in use elsewhere: ${detail}.`;
    case 422:
      return `The server rejected the change: ${detail}. Adjust the highlighted field and try again.`;
    case 429:
      return 'Too many changes in a short time. Wait a few seconds and try again.';
    default:
      return fallback;
  }
}
