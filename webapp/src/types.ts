export type RepeatKind = '' | 'daily' | 'weekdays' | 'weekly' | 'fortnightly' | 'monthly' | 'yearly';
export type EndsMode = '' | 'never' | 'on' | 'after';

export type ScheduledMessage = {
    id: string;
    channel_id: string;
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
};

export type CreatePayload = {
    channel_id: string;
    message: string;
    send_at: string;
    timezone: string;
    repeat?: RepeatKind;
    ends_mode?: EndsMode;
    ends_on?: string;       // YYYY-MM-DD
    ends_after?: number;
};

export const PLUGIN_ID = 'com.bednarz.scheduler';
