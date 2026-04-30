import React, {useCallback, useEffect, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {cancelScheduled, fetchScheduled} from '../client';
import {emitScheduledChanged} from '../events';
import {closeScheduledList, openScheduleModal, selectPluginState} from '../redux';
import type {ScheduledMessage} from '../types';

// localeForTimezone picks an English locale that knows the IANA abbreviation
// (e.g. "AEST", "EST") for the given timezone, instead of falling back to
// offset form ("GMT+10"). The browser's own locale is unreliable — an Australian
// user on en-US Chrome would otherwise see "GMT+10" instead of "AEST".
const localeForTimezone = (tz: string): string => {
    if (tz.startsWith('Australia/')) {
        return 'en-AU';
    }
    if (tz.startsWith('Pacific/')) {
        return 'en-NZ';
    }
    if (tz.startsWith('America/')) {
        return 'en-US';
    }
    if (tz.startsWith('Europe/')) {
        return 'en-GB';
    }
    return 'en';
};

// formatLocal renders a UTC ms timestamp in the supplied IANA timezone using
// the same shape as the Go server's formatSendAt: "Thu 30 Apr 2026 at 11:45 AM AEST".
const formatLocal = (ms: number, tz: string): string => {
    const parts = new Intl.DateTimeFormat(localeForTimezone(tz), {
        weekday: 'short', day: 'numeric', month: 'short', year: 'numeric',
        hour: 'numeric', minute: '2-digit', hour12: true,
        timeZone: tz,
        timeZoneName: 'short',
    }).formatToParts(new Date(ms));
    const get = (t: string) => parts.find((p) => p.type === t)?.value ?? '';
    return `${get('weekday')} ${get('day')} ${get('month')} ${get('year')} at ${get('hour')}:${get('minute')} ${get('dayPeriod').toUpperCase()} ${get('timeZoneName')}`;
};

// describeRepeat returns the short summary shown in the Repeat column.
const describeRepeat = (m: ScheduledMessage, tz: string): string => {
    if (!m.repeat) {
        return '—';
    }
    if (m.ends_mode === 'on' && m.ends_at) {
        return `${m.repeat} until ${formatLocal(m.ends_at, tz)}`;
    }
    if (m.ends_mode === 'after' && m.ends_after) {
        const left = Math.max(0, m.ends_after - (m.occurrences ?? 0));
        return `${m.repeat} · ${left} left`;
    }
    return m.repeat;
};

// resolveUserTimezone reads the user's Mattermost-configured timezone from
// the redux store, falling back to the browser timezone.
const resolveUserTimezone = (state: any): string => {
    try {
        const userId = state?.entities?.users?.currentUserId;
        const profile = userId ? state?.entities?.users?.profiles?.[userId] : undefined;
        const tz = profile?.timezone;
        if (tz) {
            if (tz.useAutomaticTimezone === 'true' && tz.automaticTimezone) {
                return tz.automaticTimezone;
            }
            if (tz.manualTimezone) {
                return tz.manualTimezone;
            }
        }
    } catch {
        // fall through
    }
    try {
        return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
        return 'UTC';
    }
};

const ScheduledList: React.FC = () => {
    const dispatch = useDispatch();
    const {listOpen} = useSelector(selectPluginState);
    const channels = useSelector((s: any) => s?.entities?.channels?.channels as Record<string, {display_name?: string; name?: string}> | undefined);
    const userTz = useSelector(resolveUserTimezone);

    const [items, setItems] = useState<ScheduledMessage[]>([]);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const reload = useCallback(async () => {
        setLoading(true);
        setError(null);
        try {
            const data = await fetchScheduled();
            data.sort((a, b) => a.send_at - b.send_at);
            setItems(data);
        } catch (e: any) {
            setError(e?.message ?? 'Failed to load');
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        if (listOpen) {
            reload();
        }
    }, [listOpen, reload]);

    if (!listOpen) {
        return null;
    }

    const onCancel = async (id: string) => {
        try {
            await cancelScheduled(id);
            setItems((prev) => prev.filter((i) => i.id !== id));
            emitScheduledChanged();
        } catch (e: any) {
            setError(e?.message ?? 'Cancel failed');
        }
    };

    const channelName = (id: string) => {
        const ch = channels?.[id];
        return ch?.display_name || ch?.name || id;
    };

    const onClose = () => dispatch(closeScheduledList());

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
                <div style={{display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8}}>
                    <h3 style={{margin: 0}}>{'Scheduled messages'}</h3>
                    <div style={{display: 'flex', gap: 8}}>
                        <button
                            type='button'
                            className='btn btn-primary'
                            onClick={() => {
                                dispatch(closeScheduledList());
                                dispatch(openScheduleModal(''));
                            }}
                        >
                            {'New scheduled message'}
                        </button>
                        <button
                            type='button'
                            className='btn btn-tertiary'
                            onClick={reload}
                            disabled={loading}
                        >
                            {loading ? 'Loading…' : 'Refresh'}
                        </button>
                    </div>
                </div>

                {error && <div style={errorStyle}>{error}</div>}

                {!loading && items.length === 0 && (
                    <div style={emptyStyle}>{'No pending scheduled messages.'}</div>
                )}

                {items.length > 0 && (
                    <table style={tableStyle}>
                        <thead>
                            <tr>
                                <th style={thStyle}>{'When'}</th>
                                <th style={thStyle}>{'Channel'}</th>
                                <th style={thStyle}>{'Repeat'}</th>
                                <th style={thStyle}>{'Message'}</th>
                                <th style={thStyle}>{'Status'}</th>
                                <th style={thStyle}/>
                            </tr>
                        </thead>
                        <tbody>
                            {items.map((m) => {
                                const completed = m.status === 'completed';
                                const rowTz = m.tz || userTz;
                                return (
                                    <tr key={m.id} style={completed ? completedRowStyle : undefined}>
                                        <td style={tdStyle}>{formatLocal(m.send_at, rowTz)}</td>
                                        <td style={tdStyle}>{channelName(m.channel_id)}</td>
                                        <td style={tdStyle}>{describeRepeat(m, rowTz)}</td>
                                        <td style={tdStyle}>{m.message.length > 80 ? m.message.slice(0, 80) + '…' : m.message}</td>
                                        <td style={tdStyle}>{m.status}</td>
                                        <td style={tdStyle}>
                                            {!completed && (
                                                <button
                                                    type='button'
                                                    className='btn btn-tertiary'
                                                    onClick={() => onCancel(m.id)}
                                                >
                                                    {'Cancel'}
                                                </button>
                                            )}
                                        </td>
                                    </tr>
                                );
                            })}
                        </tbody>
                    </table>
                )}

                <div style={{display: 'flex', justifyContent: 'flex-end', marginTop: 16}}>
                    <button
                        type='button'
                        className='btn btn-primary'
                        onClick={onClose}
                    >
                        {'Close'}
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
    minWidth: 560,
    maxWidth: 800,
    width: '90%',
    maxHeight: '80vh',
    overflowY: 'auto',
    boxShadow: '0 10px 40px rgba(0,0,0,0.25)',
};

const tableStyle: React.CSSProperties = {
    width: '100%',
    marginTop: 16,
    borderCollapse: 'collapse',
    fontSize: 13,
};

const thStyle: React.CSSProperties = {
    textAlign: 'left',
    padding: '8px 6px',
    borderBottom: '1px solid rgba(63,67,80,0.16)',
    fontWeight: 600,
};

const tdStyle: React.CSSProperties = {
    padding: '8px 6px',
    borderBottom: '1px solid rgba(63,67,80,0.08)',
    verticalAlign: 'top',
};

const emptyStyle: React.CSSProperties = {
    marginTop: 24,
    padding: 16,
    background: 'rgba(63,67,80,0.04)',
    borderRadius: 4,
    textAlign: 'center',
    fontSize: 13,
    color: 'rgba(63,67,80,0.72)',
};

const errorStyle: React.CSSProperties = {
    marginTop: 12,
    color: '#d24b4e',
    fontSize: 13,
};

const completedRowStyle: React.CSSProperties = {
    opacity: 0.55,
    fontStyle: 'italic',
};

export default ScheduledList;
