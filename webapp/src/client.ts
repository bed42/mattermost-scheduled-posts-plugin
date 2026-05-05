import {PLUGIN_ID, ScheduledMessage, CreatePayload, UpdatePayload} from './types';

const BASE = `/plugins/${PLUGIN_ID}/api`;

function csrfToken(): string {
    const match = document.cookie.match(/(?:^|;\s*)MMCSRF=([^;]+)/);
    return match ? decodeURIComponent(match[1]) : '';
}

async function request<T>(
    path: string,
    init: RequestInit = {},
    parseJson = true,
): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set('X-Requested-With', 'XMLHttpRequest');
    if (init.method && init.method !== 'GET') {
        headers.set('X-CSRF-Token', csrfToken());
        if (init.body && !headers.has('Content-Type')) {
            headers.set('Content-Type', 'application/json');
        }
    }

    const res = await fetch(`${BASE}${path}`, {
        credentials: 'include',
        ...init,
        headers,
    });

    if (!res.ok) {
        const text = await res.text().catch(() => '');
        throw new Error(text || `${res.status} ${res.statusText}`);
    }

    if (!parseJson || res.status === 204) {
        return undefined as unknown as T;
    }
    return res.json() as Promise<T>;
}

export const fetchScheduled = (): Promise<ScheduledMessage[]> =>
    request<ScheduledMessage[]>('/list', {method: 'GET'});

export const createScheduled = (payload: CreatePayload): Promise<ScheduledMessage> =>
    request<ScheduledMessage>('/create', {
        method: 'POST',
        body: JSON.stringify(payload),
    });

export const updateScheduled = (payload: UpdatePayload): Promise<ScheduledMessage> =>
    request<ScheduledMessage>('/update', {
        method: 'PATCH',
        body: JSON.stringify(payload),
    });

export const cancelScheduled = (id: string): Promise<void> =>
    request<void>('/cancel', {
        method: 'DELETE',
        body: JSON.stringify({id}),
    }, false);
