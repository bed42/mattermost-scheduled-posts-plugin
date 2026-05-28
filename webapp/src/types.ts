export type RepeatKind = '' | 'daily' | 'weekdays' | 'weekly' | 'fortnightly' | 'monthly' | 'yearly';
export type EndsMode = '' | 'never' | 'on' | 'after';

export type ScheduledMessage = {
    id: string;
    channel_ids: string[];
    user_id: string;
    message: string;
    send_at: number;
    created_at: number;
    status: 'pending' | 'sent' | 'failed' | 'completed';
    attempts?: number;
    last_error?: string;
    repeat?: RepeatKind;
    tz?: string;
    ends_mode?: EndsMode;
    ends_at?: number;
    ends_after?: number;
    occurrences?: number;
    window_ms?: number;
    fire_at?: number;
    messages?: string[];
    message_cycle?: number[];
    message_cycle_pos?: number;
    last_sent_index?: number | null;
};

export type CreatePayload = {
    channel_ids: string[];
    message: string;
    send_at: string;
    timezone: string;
    repeat?: RepeatKind;
    ends_mode?: EndsMode;
    ends_on?: string;       // YYYY-MM-DD
    ends_after?: number;
    window_ms?: number;
    messages?: string[];
};

export type UpdatePayload = CreatePayload & {id: string};

export const PLUGIN_ID = 'com.bednarz.scheduler';
