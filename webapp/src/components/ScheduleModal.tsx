import React, {useEffect, useMemo, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {createScheduled, updateScheduled} from '../client';
import {emitScheduledChanged} from '../events';
import {closeScheduleModal, selectPluginState} from '../redux';
import type {EndsMode, RepeatKind} from '../types';

const browserTimezone = (): string => {
    try {
        return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
        return 'UTC';
    }
};

const pad = (n: number) => n.toString().padStart(2, '0');

// nextHour returns YYYY-MM-DD and HH:MM strings for 1 hour from now in local time.
const nextHour = (): {date: string; time: string} => {
    const d = new Date();
    d.setMinutes(0, 0, 0);
    d.setHours(d.getHours() + 1);
    return {
        date: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`,
        time: `${pad(d.getHours())}:${pad(d.getMinutes())}`,
    };
};

const todayStr = (): string => {
    const d = new Date();
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
};

// localPartsInTimezone formats a UTC ms timestamp as wall-clock YYYY-MM-DD and
// HH:MM strings in the given IANA timezone — exactly the shape the date/time
// inputs expect, so we can re-seed the form when editing an existing schedule.
const localPartsInTimezone = (ms: number, tz: string): {date: string; time: string} => {
    const parts = new Intl.DateTimeFormat('en-CA', {
        year: 'numeric', month: '2-digit', day: '2-digit',
        hour: '2-digit', minute: '2-digit', hour12: false,
        timeZone: tz,
    }).formatToParts(new Date(ms));
    const get = (t: string) => parts.find((p) => p.type === t)?.value ?? '';
    return {
        date: `${get('year')}-${get('month')}-${get('day')}`,
        time: `${get('hour')}:${get('minute')}`,
    };
};

// commonTimezones is a small curated list. Users can also paste any IANA name.
const commonTimezones = [
    'UTC',
    'America/New_York',
    'America/Los_Angeles',
    'America/Chicago',
    'Europe/London',
    'Europe/Berlin',
    'Asia/Tokyo',
    'Asia/Singapore',
    'Australia/Sydney',
    'Pacific/Auckland',
];

const buildTzOptions = (): string[] => {
    const set = new Set(commonTimezones);
    set.add(browserTimezone());
    return Array.from(set).sort();
};

const ScheduleModal: React.FC = () => {
    const dispatch = useDispatch();
    const {modalOpen, initialMessage, editing} = useSelector(selectPluginState);
    const currentChannelId = useSelector((s: any) => s?.entities?.channels?.currentChannelId as string | undefined);

    const seed = useMemo(nextHour, []);
    const [message, setMessage] = useState('');
    const [date, setDate] = useState(seed.date);
    const [time, setTime] = useState(seed.time);
    const [tz, setTz] = useState(browserTimezone());
    const [repeat, setRepeat] = useState<RepeatKind>('');
    const [endsMode, setEndsMode] = useState<EndsMode>('never');
    const [endsOn, setEndsOn] = useState('');
    const [endsAfter, setEndsAfter] = useState(10);
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const tzOptions = useMemo(buildTzOptions, []);

    useEffect(() => {
        if (!modalOpen) {
            return;
        }
        if (editing) {
            const seedTz = editing.tz || browserTimezone();
            const {date: ed, time: et} = localPartsInTimezone(editing.send_at, seedTz);
            setMessage(editing.message);
            setDate(ed);
            setTime(et);
            setTz(seedTz);
            setRepeat(editing.repeat ?? '');
            setEndsMode((editing.ends_mode as EndsMode) || 'never');
            setEndsOn(editing.ends_at ? localPartsInTimezone(editing.ends_at, seedTz).date : '');
            setEndsAfter(editing.ends_after ?? 10);
        } else {
            const seeded = nextHour();
            setMessage(initialMessage ?? '');
            setDate(seeded.date);
            setTime(seeded.time);
            setTz(browserTimezone());
            setRepeat('');
            setEndsMode('never');
            setEndsOn('');
            setEndsAfter(10);
        }
        setError(null);
    }, [modalOpen, initialMessage, editing]);

    if (!modalOpen) {
        return null;
    }

    let preview = '';
    try {
        const local = new Date(`${date}T${time}:00`);
        preview = local.toLocaleString(undefined, {
            weekday: 'long', day: 'numeric', month: 'long', year: 'numeric',
            hour: 'numeric', minute: '2-digit', timeZone: tz, timeZoneName: 'short',
        });
    } catch {
        preview = '';
    }

    // describeRepeat builds a human-readable explanation of *exactly* what the
    // chosen recurrence will do — pulling the weekday / day-of-month / month
    // out of the chosen send date so the user sees, e.g., "every Friday at
    // 09:00 Australia/Sydney" rather than the bare word "weekly".
    const describeRepeat = (): string => {
        if (!repeat) {
            return '';
        }
        const local = new Date(`${date}T${time}:00`);
        if (isNaN(local.getTime())) {
            return '';
        }
        const weekday = new Intl.DateTimeFormat(undefined, {weekday: 'long', timeZone: tz}).format(local);
        const monthName = new Intl.DateTimeFormat(undefined, {month: 'long', timeZone: tz}).format(local);
        const dayOfMonth = parseInt(date.split('-')[2] ?? '', 10);
        const ordinal = (n: number): string => {
            if (n >= 11 && n <= 13) {
                return `${n}th`;
            }
            switch (n % 10) {
            case 1: return `${n}st`;
            case 2: return `${n}nd`;
            case 3: return `${n}rd`;
            default: return `${n}th`;
            }
        };
        const at = `at ${time} ${tz}`;

        let body: string;
        switch (repeat) {
        case 'daily':
            body = `Sends every day ${at}.`;
            break;
        case 'weekdays':
            body = `Sends every weekday (Monday through Friday) ${at}.`;
            break;
        case 'weekly':
            body = `Sends every ${weekday} ${at}.`;
            break;
        case 'fortnightly':
            body = `Sends every other ${weekday} ${at} (once every two weeks).`;
            break;
        case 'monthly':
            body = `Sends on the ${ordinal(dayOfMonth)} of every month ${at}. Months without a ${ordinal(dayOfMonth)} fall back to the last day of the month.`;
            break;
        case 'yearly':
            body = `Sends every year on ${monthName} ${dayOfMonth} ${at}.`;
            if (date.endsWith('-02-29')) {
                body += ' Feb 29 falls back to Feb 28 in non-leap years.';
            }
            break;
        default:
            body = `Repeats ${repeat}.`;
        }

        if (endsMode === 'on' && endsOn) {
            body += ` Stops after ${endsOn}.`;
        } else if (endsMode === 'after' && endsAfter > 0) {
            body += ` Stops after ${endsAfter} occurrence${endsAfter === 1 ? '' : 's'}.`;
        }
        return body;
    };

    const repeatPreview = describeRepeat();

    const onSubmit = async () => {
        setError(null);
        if (!message.trim()) {
            setError('Please enter a message.');
            return;
        }
        const channelId = editing ? editing.channel_id : currentChannelId;
        if (!channelId) {
            setError('No channel selected.');
            return;
        }

        if (repeat && endsMode === 'on' && !endsOn) {
            setError('Pick an end date or change "Ends" to "Never" / "After".');
            return;
        }
        if (repeat && endsMode === 'after' && (!endsAfter || endsAfter < 1)) {
            setError('Occurrence count must be at least 1.');
            return;
        }

        // Build the local datetime and let the server combine it with the timezone.
        const sendAt = `${date}T${time}:00`;
        const payload = {
            channel_id: channelId,
            message: message.trim(),
            send_at: sendAt,
            timezone: tz,
            repeat: repeat || undefined,
            ends_mode: repeat ? endsMode : undefined,
            ends_on: repeat && endsMode === 'on' ? endsOn : undefined,
            ends_after: repeat && endsMode === 'after' ? endsAfter : undefined,
        };
        setSubmitting(true);
        try {
            if (editing) {
                await updateScheduled({...payload, id: editing.id});
            } else {
                await createScheduled(payload);
            }
            emitScheduledChanged();
            dispatch(closeScheduleModal());
        } catch (e: any) {
            setError(e?.message ?? 'Failed to schedule message');
        } finally {
            setSubmitting(false);
        }
    };

    const onClose = () => dispatch(closeScheduleModal());

    return (
        <div
            role='dialog'
            aria-modal='true'
            style={overlayStyle}
            onClick={onClose}
        >
            <div
                style={modalStyle}
                onClick={(e) => e.stopPropagation()}
            >
                <h3 style={{marginTop: 0}}>{editing ? 'Edit scheduled message' : 'Schedule a message'}</h3>

                <label style={labelStyle}>{'Message'}</label>
                <textarea
                    value={message}
                    onChange={(e) => setMessage(e.target.value)}
                    rows={4}
                    style={textareaStyle}
                    placeholder='What do you want to say?'
                    autoFocus={true}
                />

                <div style={{display: 'flex', gap: 12, marginTop: 12}}>
                    <div style={{flex: 1}}>
                        <label style={labelStyle}>{'Date'}</label>
                        <input
                            type='date'
                            value={date}
                            min={todayStr()}
                            onChange={(e) => setDate(e.target.value)}
                            style={inputStyle}
                        />
                    </div>
                    <div style={{flex: 1}}>
                        <label style={labelStyle}>{'Time'}</label>
                        <input
                            type='time'
                            value={time}
                            onChange={(e) => setTime(e.target.value)}
                            style={inputStyle}
                        />
                    </div>
                </div>

                <label style={labelStyle}>{'Timezone'}</label>
                <select
                    value={tz}
                    onChange={(e) => setTz(e.target.value)}
                    style={inputStyle}
                >
                    {tzOptions.map((z) => (
                        <option key={z} value={z}>{z}</option>
                    ))}
                </select>

                <label style={labelStyle}>{'Repeat'}</label>
                <select
                    value={repeat}
                    onChange={(e) => setRepeat(e.target.value as RepeatKind)}
                    style={inputStyle}
                >
                    <option value=''>{'Does not repeat'}</option>
                    <option value='daily'>{'Daily'}</option>
                    <option value='weekdays'>{'Every weekday (Mon–Fri)'}</option>
                    <option value='weekly'>{'Weekly'}</option>
                    <option value='fortnightly'>{'Fortnightly'}</option>
                    <option value='monthly'>{'Monthly'}</option>
                    <option value='yearly'>{'Yearly'}</option>
                </select>

                {repeat && (
                    <>
                        <label style={labelStyle}>{'Ends'}</label>
                        <div style={{display: 'flex', gap: 12}}>
                            <select
                                value={endsMode}
                                onChange={(e) => setEndsMode(e.target.value as EndsMode)}
                                style={{...inputStyle, flex: 1}}
                            >
                                <option value='never'>{'Never'}</option>
                                <option value='on'>{'On date'}</option>
                                <option value='after'>{'After N occurrences'}</option>
                            </select>
                            {endsMode === 'on' && (
                                <input
                                    type='date'
                                    value={endsOn}
                                    min={date}
                                    onChange={(e) => setEndsOn(e.target.value)}
                                    style={{...inputStyle, flex: 1}}
                                />
                            )}
                            {endsMode === 'after' && (
                                <input
                                    type='number'
                                    min={1}
                                    value={endsAfter}
                                    onChange={(e) => setEndsAfter(parseInt(e.target.value, 10) || 0)}
                                    style={{...inputStyle, flex: 1}}
                                />
                            )}
                        </div>
                    </>
                )}

                {preview && <div style={previewStyle}>{`Will send: ${preview}`}</div>}
                {repeatPreview && <div style={previewStyle}>{repeatPreview}</div>}
                {error && <div style={errorStyle}>{error}</div>}

                <div style={{display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 18}}>
                    <button
                        type='button'
                        className='btn btn-tertiary'
                        onClick={onClose}
                        disabled={submitting}
                    >
                        {'Cancel'}
                    </button>
                    <button
                        type='button'
                        className='btn btn-primary'
                        onClick={onSubmit}
                        disabled={submitting}
                    >
                        {submitting ? (editing ? 'Saving…' : 'Scheduling…') : (editing ? 'Save' : 'Schedule')}
                    </button>
                </div>
            </div>
        </div>
    );
};

const overlayStyle: React.CSSProperties = {
    position: 'fixed',
    inset: 0,
    background: 'rgba(0,0,0,0.45)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 10000,
};

const modalStyle: React.CSSProperties = {
    background: 'var(--center-channel-bg, #fff)',
    color: 'var(--center-channel-color, #1c1c1c)',
    padding: 24,
    borderRadius: 8,
    minWidth: 420,
    maxWidth: 540,
    width: '90%',
    boxShadow: '0 10px 40px rgba(0,0,0,0.25)',
};

const labelStyle: React.CSSProperties = {
    display: 'block',
    fontSize: 12,
    fontWeight: 600,
    marginTop: 12,
    marginBottom: 4,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
};

const inputStyle: React.CSSProperties = {
    width: '100%',
    padding: '8px 10px',
    border: '1px solid rgba(63,67,80,0.16)',
    borderRadius: 4,
    background: 'var(--center-channel-bg, #fff)',
    color: 'inherit',
    fontSize: 14,
};

const textareaStyle: React.CSSProperties = {
    ...inputStyle,
    resize: 'vertical',
    fontFamily: 'inherit',
};

const previewStyle: React.CSSProperties = {
    marginTop: 12,
    padding: '8px 10px',
    background: 'rgba(63,67,80,0.06)',
    borderRadius: 4,
    fontSize: 13,
};

const errorStyle: React.CSSProperties = {
    marginTop: 12,
    color: '#d24b4e',
    fontSize: 13,
};

export default ScheduleModal;
