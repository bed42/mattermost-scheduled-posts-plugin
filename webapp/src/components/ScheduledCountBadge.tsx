import React, {useEffect, useState} from 'react';
import {useDispatch} from 'react-redux';

import {fetchScheduled} from '../client';
import {SCHEDULED_CHANGED_EVENT} from '../events';
import {openScheduledList} from '../redux';

const ClockSvg: React.FC = () => (
    <svg
        width='18'
        height='18'
        viewBox='0 0 24 24'
        fill='none'
        xmlns='http://www.w3.org/2000/svg'
        aria-hidden='true'
    >
        <circle cx='12' cy='12' r='9' stroke='currentColor' strokeWidth='1.8'/>
        <path d='M12 7v5l3 2' stroke='currentColor' strokeWidth='1.8' strokeLinecap='round' strokeLinejoin='round'/>
    </svg>
);

// Mattermost's SidebarLink CSS doesn't apply when the component is mounted
// via registerLeftSidebarHeaderComponent (out of the SidebarChannelGroup
// selector scope), so we replicate the metrics inline. The classNames are
// kept so any matching descendant selectors can still style us.
const IMMINENT_WINDOW_MS = 24 * 60 * 60 * 1000;

const ScheduledCountBadge: React.FC = () => {
    const dispatch = useDispatch();
    const [count, setCount] = useState<number | null>(null);
    const [soonestSendAt, setSoonestSendAt] = useState<number | null>(null);

    useEffect(() => {
        let cancelled = false;
        const load = async () => {
            try {
                const items = await fetchScheduled();
                if (cancelled) {
                    return;
                }
                const pending = items.filter((i) => i.status === 'pending');
                setCount(pending.length);
                setSoonestSendAt(pending.length === 0 ? null : Math.min(...pending.map((p) => p.send_at)));
            } catch {
                if (!cancelled) {
                    setCount(null);
                    setSoonestSendAt(null);
                }
            }
        };
        load();
        const interval = setInterval(load, 30_000);
        const onChanged = () => load();
        window.addEventListener(SCHEDULED_CHANGED_EVENT, onChanged);
        return () => {
            cancelled = true;
            clearInterval(interval);
            window.removeEventListener(SCHEDULED_CHANGED_EVENT, onChanged);
        };
    }, []);

    const hasPending = count != null && count > 0;
    // Imminent = at least one pending message scheduled within the next 24h.
    // Drives the "loud" styling: bold label, bright mention-coloured badge.
    const imminent = hasPending && soonestSendAt != null && soonestSendAt - Date.now() < IMMINENT_WINDOW_MS;

    const onClick = (e: React.MouseEvent) => {
        e.preventDefault();
        dispatch(openScheduledList());
    };

    return (
        <ul
            className='SidebarChannelGroup'
            style={{listStyle: 'none', padding: 0, margin: 0}}
        >
            <li
                className='SidebarChannel'
                tabIndex={-1}
                style={{listStyle: 'none', margin: 0}}
            >
                <a
                    className='SidebarLink sidebar-item'
                    tabIndex={0}
                    href='#'
                    onClick={onClick}
                    title='View scheduled messages'
                    style={{
                        display: 'flex',
                        alignItems: 'center',
                        height: 32,
                        padding: '7px 16px 7px 19px',
                        textDecoration: 'none',
                        color: 'var(--sidebar-text, rgba(255,255,255,0.72))',
                        opacity: imminent ? 1 : 0.64,
                        fontSize: 14,
                        fontWeight: imminent ? 600 : 400,
                        cursor: 'pointer',
                    }}
                >
                    <span
                        className='icon'
                        style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            padding: 3,
                            marginRight: 7,
                            marginLeft: -1,
                            flexShrink: 0,
                            lineHeight: 0,
                        }}
                    >
                        <ClockSvg/>
                    </span>
                    <div
                        className='SidebarChannelLinkLabel_wrapper'
                        style={{flex: 1, overflow: 'hidden'}}
                    >
                        <span
                            className='SidebarChannelLinkLabel sidebar-item__name'
                            style={{whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis'}}
                        >
                            {'Scheduled'}
                        </span>
                    </div>
                    {hasPending && (
                        <span
                            style={{
                                marginLeft: 'auto',
                                // When imminent: full mention-bg colour.
                                // Otherwise: muted, low-contrast pill.
                                background: imminent
                                    ? 'var(--mention-bg, #fff)'
                                    : 'rgba(255,255,255,0.16)',
                                color: imminent
                                    ? 'var(--mention-color, #1c58d9)'
                                    : 'var(--sidebar-text, rgba(255,255,255,0.72))',
                                borderRadius: 10,
                                padding: '1px 7px',
                                fontSize: 11,
                                minWidth: 16,
                                textAlign: 'center',
                                fontWeight: 700,
                            }}
                        >
                            {count}
                        </span>
                    )}
                </a>
            </li>
        </ul>
    );
};

export default ScheduledCountBadge;
