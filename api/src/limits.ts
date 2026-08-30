/** Portable per-request/per-event ceiling, comfortably below D1's 2 MiB row limit. */
export const MAX_EVENT_BYTES = 256 * 1024;

/** Keep list queries bounded even when events are tiny. */
export const MAX_EVENTS_PAGE_LIMIT = 100;

/** Hard UTF-8 JSON budget for one event-list response. */
export const MAX_EVENTS_RESPONSE_BYTES = 4 * 1024 * 1024;
