import React, {useEffect, useMemo, useRef, useState} from 'react';
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

type ChannelOption = {id: string; label: string; type: string};

const ScheduleModal: React.FC = () => {
    const dispatch = useDispatch();
    const {modalOpen, initialMessage, editing} = useSelector(selectPluginState);
    const currentChannelId = useSelector((s: any) => s?.entities?.channels?.currentChannelId as string | undefined);
    const allChannels = useSelector((s: any) => s?.entities?.channels?.channels as Record<string, any> | undefined);
    const myMembers = useSelector((s: any) => s?.entities?.channels?.myMembers as Record<string, any> | undefined);
    const currentTeamId = useSelector((s: any) => s?.entities?.teams?.currentTeamId as string | undefined);
    const profiles = useSelector((s: any) => s?.entities?.users?.profiles as Record<string, any> | undefined);
    const currentUserId = useSelector((s: any) => s?.entities?.users?.currentUserId as string | undefined);

    const channelOptions = useMemo<ChannelOption[]>(() => {
        if (!allChannels || !myMembers) {
            return [];
        }
        const opts: ChannelOption[] = [];
        for (const ch of Object.values<any>(allChannels)) {
            if (!myMembers[ch.id]) {
                continue;
            }
            if (ch.delete_at && ch.delete_at > 0) {
                continue;
            }
            let label = '';
            if (ch.type === 'D') {
                let teammateId = ch.teammate_id;
                if (!teammateId && ch.name && currentUserId) {
                    const parts = ch.name.split('__');
                    teammateId = parts.find((p: string) => p !== currentUserId) || parts[0];
                }
                const p = teammateId ? profiles?.[teammateId] : undefined;
                label = p?.username ? `@${p.username}` : (ch.display_name || ch.name || ch.id);
            } else if (ch.type === 'G') {
                label = ch.display_name ? ch.display_name.split(',').map((s: string) => `@${s.trim()}`).join(', ') : (ch.name || ch.id);
            } else {
                // Public/private channels: restrict to current team so we don't surface every team's channels.
                if (currentTeamId && ch.team_id && ch.team_id !== currentTeamId) {
                    continue;
                }
                const prefix = ch.type === 'P' ? '🔒 ' : '# ';
                label = `${prefix}${ch.display_name || ch.name || ch.id}`;
            }
            opts.push({id: ch.id, label, type: ch.type});
        }
        opts.sort((a, b) => {
            const typeRank = (t: string) => (t === 'O' || t === 'P' ? 0 : 1);
            const r = typeRank(a.type) - typeRank(b.type);
            if (r !== 0) {
                return r;
            }
            return a.label.localeCompare(b.label);
        });
        return opts;
    }, [allChannels, myMembers, currentTeamId, profiles, currentUserId]);

    const channelLabelById = (id: string): string => {
        const opt = channelOptions.find((o) => o.id === id);
        return opt?.label ?? id;
    };

    const [channelId, setChannelId] = useState<string>('');
    const [channelQuery, setChannelQuery] = useState<string>('');
    const [channelMenuOpen, setChannelMenuOpen] = useState(false);
    const [highlightedIndex, setHighlightedIndex] = useState(0);
    const channelPickerRef = useRef<HTMLDivElement>(null);
    const channelListRef = useRef<HTMLUListElement>(null);

    const seed = useMemo(nextHour, []);
    const [messages, setMessages] = useState<string[]>(['']);
    const [date, setDate] = useState(seed.date);
    const [time, setTime] = useState(seed.time);
    const [useRange, setUseRange] = useState(false);
    const [endTime, setEndTime] = useState(seed.time);
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
        const seededChannelId = editing ? editing.channel_id : (currentChannelId ?? '');
        setChannelId(seededChannelId);
        setChannelQuery(seededChannelId ? channelLabelById(seededChannelId) : '');
        setChannelMenuOpen(false);
        if (editing) {
            const seedTz = editing.tz || browserTimezone();
            const {date: ed, time: et} = localPartsInTimezone(editing.send_at, seedTz);
            const list = (editing.messages && editing.messages.length > 0) ? editing.messages : [editing.message];
            setMessages(list);
            setDate(ed);
            setTime(et);
            const windowMs = editing.window_ms ?? 0;
            setUseRange(windowMs > 0);
            if (windowMs > 0) {
                const endMs = editing.send_at + windowMs;
                setEndTime(localPartsInTimezone(endMs, seedTz).time);
            } else {
                setEndTime(et);
            }
            setTz(seedTz);
            setRepeat(editing.repeat ?? '');
            setEndsMode((editing.ends_mode as EndsMode) || 'never');
            setEndsOn(editing.ends_at ? localPartsInTimezone(editing.ends_at, seedTz).date : '');
            setEndsAfter(editing.ends_after ?? 10);
        } else {
            const seeded = nextHour();
            setMessages([initialMessage ?? '']);
            setDate(seeded.date);
            setTime(seeded.time);
            setUseRange(false);
            setEndTime(seeded.time);
            setTz(browserTimezone());
            setRepeat('');
            setEndsMode('never');
            setEndsOn('');
            setEndsAfter(10);
        }
        setError(null);
    }, [modalOpen, initialMessage, editing]);

    // Trim multi-message list when leaving recurring mode — multi-message
    // rotations only make sense for repeating schedules.
    useEffect(() => {
        if (!repeat && messages.length > 1) {
            setMessages([messages[0] ?? '']);
        }
    }, [repeat]); // eslint-disable-line react-hooks/exhaustive-deps

    // Keep the channel input in sync with the selected channel's label
    // whenever the menu is closed (covers late-loading channel data and
    // post-selection state).
    useEffect(() => {
        if (channelMenuOpen) {
            return;
        }
        if (channelId) {
            setChannelQuery(channelLabelById(channelId));
        }
    }, [channelId, channelOptions, channelMenuOpen]); // eslint-disable-line react-hooks/exhaustive-deps

    useEffect(() => {
        if (!channelMenuOpen) {
            return undefined;
        }
        const onDocClick = (e: MouseEvent) => {
            if (channelPickerRef.current && !channelPickerRef.current.contains(e.target as Node)) {
                setChannelMenuOpen(false);
            }
        };
        document.addEventListener('mousedown', onDocClick);
        return () => document.removeEventListener('mousedown', onDocClick);
    }, [channelMenuOpen]);

    const filteredChannels = useMemo<ChannelOption[]>(() => {
        const selectedLabel = channelId ? channelLabelById(channelId) : '';
        const q = channelQuery.trim().toLowerCase();
        // If query matches the selected label exactly, show the full list — the
        // user just opened the menu without typing anything new.
        if (!q || channelQuery === selectedLabel) {
            return channelOptions.slice(0, 100);
        }
        return channelOptions.filter((o) => o.label.toLowerCase().includes(q)).slice(0, 100);
    }, [channelOptions, channelQuery, channelId]); // eslint-disable-line react-hooks/exhaustive-deps

    // When the menu opens, point the highlight at the currently selected
    // channel (if visible) so up/down feels natural; otherwise highlight the
    // first match. When the filter changes, clamp the highlight back to 0.
    useEffect(() => {
        if (!channelMenuOpen) {
            return;
        }
        const i = filteredChannels.findIndex((o) => o.id === channelId);
        setHighlightedIndex(i >= 0 ? i : 0);
    }, [channelMenuOpen, filteredChannels, channelId]);

    // Keep the highlighted option in view as the user arrows up and down.
    useEffect(() => {
        if (!channelMenuOpen) {
            return;
        }
        const list = channelListRef.current;
        const item = list?.children[highlightedIndex] as HTMLElement | undefined;
        item?.scrollIntoView({block: 'nearest'});
    }, [highlightedIndex, channelMenuOpen]);

    if (!modalOpen) {
        return null;
    }

    const parseHHMM = (s: string): number | null => {
        const m = /^(\d{1,2}):(\d{2})$/.exec(s);
        if (!m) {
            return null;
        }
        const h = parseInt(m[1], 10);
        const mm = parseInt(m[2], 10);
        if (h < 0 || h > 23 || mm < 0 || mm > 59) {
            return null;
        }
        return (h * 60) + mm;
    };
    const startMins = parseHHMM(time);
    const endMins = parseHHMM(endTime);
    const windowMinutes = (useRange && startMins != null && endMins != null && endMins > startMins)
        ? endMins - startMins
        : 0;

    const formatTimeOfDay = (hhmm: string): string => {
        try {
            const local = new Date(`${date}T${hhmm}:00`);
            return local.toLocaleString(undefined, {
                hour: 'numeric', minute: '2-digit', timeZone: tz,
            });
        } catch {
            return hhmm;
        }
    };

    let preview = '';
    try {
        const local = new Date(`${date}T${time}:00`);
        const startStr = local.toLocaleString(undefined, {
            weekday: 'long', day: 'numeric', month: 'long', year: 'numeric',
            hour: 'numeric', minute: '2-digit', timeZone: tz, timeZoneName: 'short',
        });
        if (useRange && windowMinutes > 0) {
            preview = `${startStr} — random within ${formatTimeOfDay(time)}–${formatTimeOfDay(endTime)}`;
        } else {
            preview = startStr;
        }
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
        const at = (useRange && windowMinutes > 0)
            ? `between ${time} and ${endTime} ${tz}`
            : `at ${time} ${tz}`;

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
        const trimmed = messages.map((m) => m.trim()).filter((m) => m !== '');
        if (trimmed.length === 0) {
            setError('Please enter a message.');
            return;
        }
        const targetChannelId = editing ? editing.channel_id : channelId;
        if (!targetChannelId) {
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
        if (useRange) {
            if (startMins == null || endMins == null) {
                setError('Time range needs a valid start and end.');
                return;
            }
            if (endMins <= startMins) {
                setError('Range end must be after the start time.');
                return;
            }
        }
        if (trimmed.length > 1 && !repeat) {
            setError('Multiple messages need a repeating schedule.');
            return;
        }

        // Build the local datetime and let the server combine it with the timezone.
        const sendAt = `${date}T${time}:00`;
        const useMessagesArray = repeat && trimmed.length >= 2;
        const payload = {
            channel_id: targetChannelId,
            message: trimmed[0],
            send_at: sendAt,
            timezone: tz,
            repeat: repeat || undefined,
            ends_mode: repeat ? endsMode : undefined,
            ends_on: repeat && endsMode === 'on' ? endsOn : undefined,
            ends_after: repeat && endsMode === 'after' ? endsAfter : undefined,
            window_ms: useRange && windowMinutes > 0 ? windowMinutes * 60_000 : undefined,
            messages: useMessagesArray ? trimmed : undefined,
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

                <label style={labelStyle}>{'Send to'}</label>
                {editing ? (
                    <input
                        type='text'
                        value={channelLabelById(editing.channel_id)}
                        disabled={true}
                        style={inputStyle}
                    />
                ) : (
                    <div
                        ref={channelPickerRef}
                        style={{position: 'relative'}}
                    >
                        <input
                            type='text'
                            value={channelQuery}
                            placeholder='Type a channel name or @user'
                            role='combobox'
                            aria-expanded={channelMenuOpen}
                            aria-controls='schedule-channel-listbox'
                            aria-activedescendant={
                                channelMenuOpen && filteredChannels[highlightedIndex] ?
                                    `schedule-channel-opt-${filteredChannels[highlightedIndex].id}` :
                                    undefined
                            }
                            onChange={(e) => {
                                setChannelQuery(e.target.value);
                                setChannelMenuOpen(true);
                            }}
                            onFocus={(e) => {
                                setChannelMenuOpen(true);
                                e.currentTarget.select();
                            }}
                            onKeyDown={(e) => {
                                if (e.key === 'ArrowDown') {
                                    e.preventDefault();
                                    if (!channelMenuOpen) {
                                        setChannelMenuOpen(true);
                                        return;
                                    }
                                    setHighlightedIndex((i) => Math.min(i + 1, filteredChannels.length - 1));
                                } else if (e.key === 'ArrowUp') {
                                    e.preventDefault();
                                    if (!channelMenuOpen) {
                                        setChannelMenuOpen(true);
                                        return;
                                    }
                                    setHighlightedIndex((i) => Math.max(i - 1, 0));
                                } else if (e.key === 'Home') {
                                    if (channelMenuOpen) {
                                        e.preventDefault();
                                        setHighlightedIndex(0);
                                    }
                                } else if (e.key === 'End') {
                                    if (channelMenuOpen) {
                                        e.preventDefault();
                                        setHighlightedIndex(filteredChannels.length - 1);
                                    }
                                } else if (e.key === 'Enter') {
                                    if (channelMenuOpen && filteredChannels[highlightedIndex]) {
                                        e.preventDefault();
                                        const o = filteredChannels[highlightedIndex];
                                        setChannelId(o.id);
                                        setChannelQuery(o.label);
                                        setChannelMenuOpen(false);
                                    }
                                } else if (e.key === 'Escape') {
                                    if (channelMenuOpen) {
                                        e.preventDefault();
                                        e.stopPropagation();
                                        setChannelMenuOpen(false);
                                        // Revert any typed-but-not-committed text.
                                        if (channelId) {
                                            setChannelQuery(channelLabelById(channelId));
                                        }
                                    }
                                } else if (e.key === 'Tab') {
                                    setChannelMenuOpen(false);
                                }
                            }}
                            style={inputStyle}
                        />
                        {channelMenuOpen && filteredChannels.length > 0 && (
                            <ul
                                id='schedule-channel-listbox'
                                role='listbox'
                                ref={channelListRef}
                                style={pickerMenuStyle}
                            >
                                {filteredChannels.map((o, idx) => (
                                    <li
                                        key={o.id}
                                        id={`schedule-channel-opt-${o.id}`}
                                        role='option'
                                        aria-selected={o.id === channelId}
                                        style={{
                                            ...pickerOptionStyle,
                                            ...(idx === highlightedIndex ? pickerOptionHighlightedStyle : null),
                                            ...(o.id === channelId ? pickerOptionSelectedStyle : null),
                                        }}
                                        onMouseEnter={() => setHighlightedIndex(idx)}
                                        onMouseDown={(e) => {
                                            // mouseDown (not click) so we beat the input's blur
                                            e.preventDefault();
                                            setChannelId(o.id);
                                            setChannelQuery(o.label);
                                            setChannelMenuOpen(false);
                                        }}
                                    >
                                        {o.label}
                                    </li>
                                ))}
                            </ul>
                        )}
                        {channelMenuOpen && filteredChannels.length === 0 && (
                            <div style={pickerEmptyStyle}>{'No matches.'}</div>
                        )}
                    </div>
                )}

                <label style={labelStyle}>
                    {messages.length > 1 ? `Messages (${messages.length}, rotating)` : 'Message'}
                </label>
                {messages.map((m, i) => (
                    <div
                        key={i}
                        style={{display: 'flex', gap: 8, alignItems: 'flex-start', marginTop: i === 0 ? 0 : 8}}
                    >
                        <textarea
                            value={m}
                            onChange={(e) => {
                                const next = messages.slice();
                                next[i] = e.target.value;
                                setMessages(next);
                            }}
                            rows={messages.length > 1 ? 2 : 4}
                            style={{...textareaStyle, flex: 1}}
                            placeholder={i === 0 ? 'What do you want to say?' : `Message ${i + 1}`}
                            autoFocus={i === 0}
                        />
                        {messages.length > 1 && (
                            <button
                                type='button'
                                onClick={() => setMessages(messages.filter((_, j) => j !== i))}
                                style={removeButtonStyle}
                                aria-label={`Remove message ${i + 1}`}
                            >
                                {'×'}
                            </button>
                        )}
                    </div>
                ))}
                {repeat && (
                    <button
                        type='button'
                        onClick={() => setMessages([...messages, ''])}
                        style={addAnotherStyle}
                    >
                        {'+ Add another message'}
                    </button>
                )}

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
                        <label style={labelStyle}>{useRange ? 'Start time' : 'Time'}</label>
                        <input
                            type='time'
                            value={time}
                            onChange={(e) => setTime(e.target.value)}
                            style={inputStyle}
                        />
                    </div>
                    {useRange && (
                        <div style={{flex: 1}}>
                            <label style={labelStyle}>{'End time'}</label>
                            <input
                                type='time'
                                value={endTime}
                                onChange={(e) => setEndTime(e.target.value)}
                                style={inputStyle}
                            />
                        </div>
                    )}
                </div>
                <label style={{...labelStyle, display: 'flex', alignItems: 'center', gap: 6, textTransform: 'none', fontWeight: 'normal', letterSpacing: 0, marginTop: 8}}>
                    <input
                        type='checkbox'
                        checked={useRange}
                        onChange={(e) => setUseRange(e.target.checked)}
                    />
                    {'Fire at a random time within a range'}
                </label>

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
    maxHeight: '85vh',
    overflowY: 'auto',
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

const removeButtonStyle: React.CSSProperties = {
    border: '1px solid rgba(63,67,80,0.16)',
    background: 'transparent',
    borderRadius: 4,
    width: 28,
    height: 28,
    cursor: 'pointer',
    fontSize: 16,
    lineHeight: 1,
    color: 'inherit',
    flexShrink: 0,
};

const pickerMenuStyle: React.CSSProperties = {
    position: 'absolute',
    top: '100%',
    left: 0,
    right: 0,
    marginTop: 2,
    maxHeight: 220,
    overflowY: 'auto',
    background: 'var(--center-channel-bg, #fff)',
    border: '1px solid rgba(63,67,80,0.16)',
    borderRadius: 4,
    boxShadow: '0 4px 12px rgba(0,0,0,0.12)',
    zIndex: 10001,
    listStyle: 'none',
    padding: 4,
    margin: 0,
};

const pickerOptionStyle: React.CSSProperties = {
    padding: '6px 10px',
    cursor: 'pointer',
    fontSize: 13,
    borderRadius: 3,
};

const pickerOptionHighlightedStyle: React.CSSProperties = {
    background: 'rgba(28,88,217,0.12)',
};

const pickerOptionSelectedStyle: React.CSSProperties = {
    fontWeight: 600,
};

const pickerEmptyStyle: React.CSSProperties = {
    position: 'absolute',
    top: '100%',
    left: 0,
    right: 0,
    marginTop: 2,
    background: 'var(--center-channel-bg, #fff)',
    border: '1px solid rgba(63,67,80,0.16)',
    borderRadius: 4,
    boxShadow: '0 4px 12px rgba(0,0,0,0.12)',
    zIndex: 10001,
    padding: '8px 10px',
    fontSize: 13,
    color: 'rgba(63,67,80,0.6)',
};

const addAnotherStyle: React.CSSProperties = {
    background: 'transparent',
    border: 'none',
    color: 'var(--link-color, #166de0)',
    padding: '6px 0',
    marginTop: 6,
    fontSize: 13,
    cursor: 'pointer',
    textAlign: 'left',
};

export default ScheduleModal;
