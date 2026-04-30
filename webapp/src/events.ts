import {PLUGIN_ID} from './types';

// Custom DOM event emitted whenever the user creates or cancels a scheduled
// message. The sidebar count badge listens on `window` and reloads — using a
// DOM event sidesteps redux Provider issues across plugin-mounted components.
export const SCHEDULED_CHANGED_EVENT = `${PLUGIN_ID}:scheduled-changed`;

export const emitScheduledChanged = (): void => {
    try {
        window.dispatchEvent(new CustomEvent(SCHEDULED_CHANGED_EVENT));
    } catch {
        // older browsers / non-DOM environments — ignore
    }
};
