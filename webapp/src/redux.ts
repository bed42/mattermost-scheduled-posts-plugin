import {PLUGIN_ID} from './types';
import type {ScheduledMessage} from './types';

export const OPEN_SCHEDULE_MODAL = `${PLUGIN_ID}/OPEN_SCHEDULE_MODAL`;
export const CLOSE_SCHEDULE_MODAL = `${PLUGIN_ID}/CLOSE_SCHEDULE_MODAL`;
export const OPEN_SCHEDULED_LIST = `${PLUGIN_ID}/OPEN_SCHEDULED_LIST`;
export const CLOSE_SCHEDULED_LIST = `${PLUGIN_ID}/CLOSE_SCHEDULED_LIST`;

export type OpenScheduleModalAction = {
    type: typeof OPEN_SCHEDULE_MODAL;
    initialMessage?: string;
    editing?: ScheduledMessage;
};
export type CloseScheduleModalAction = {type: typeof CLOSE_SCHEDULE_MODAL};
export type OpenScheduledListAction = {type: typeof OPEN_SCHEDULED_LIST};
export type CloseScheduledListAction = {type: typeof CLOSE_SCHEDULED_LIST};

type Action =
    | OpenScheduleModalAction
    | CloseScheduleModalAction
    | OpenScheduledListAction
    | CloseScheduledListAction;

export type PluginState = {
    modalOpen: boolean;
    listOpen: boolean;
    initialMessage: string;
    editing: ScheduledMessage | null;
};

const initialState: PluginState = {
    modalOpen: false,
    listOpen: false,
    initialMessage: '',
    editing: null,
};

export const reducer = (state: PluginState = initialState, action: Action): PluginState => {
    switch (action.type) {
    case OPEN_SCHEDULE_MODAL:
        return {
            ...state,
            modalOpen: true,
            initialMessage: action.initialMessage ?? '',
            editing: action.editing ?? null,
        };
    case CLOSE_SCHEDULE_MODAL:
        return {...state, modalOpen: false, initialMessage: '', editing: null};
    case OPEN_SCHEDULED_LIST:
        return {...state, listOpen: true};
    case CLOSE_SCHEDULED_LIST:
        return {...state, listOpen: false};
    default:
        return state;
    }
};

export const openScheduleModal = (initialMessage = ''): OpenScheduleModalAction => ({
    type: OPEN_SCHEDULE_MODAL,
    initialMessage,
});
export const openScheduleModalForEdit = (msg: ScheduledMessage): OpenScheduleModalAction => ({
    type: OPEN_SCHEDULE_MODAL,
    editing: msg,
});
export const closeScheduleModal = (): CloseScheduleModalAction => ({type: CLOSE_SCHEDULE_MODAL});
export const openScheduledList = (): OpenScheduledListAction => ({type: OPEN_SCHEDULED_LIST});
export const closeScheduledList = (): CloseScheduledListAction => ({type: CLOSE_SCHEDULED_LIST});

// Plugin reducers can be mounted under either `state['plugins-<id>']` or
// `state.plugins[<id>]` depending on Mattermost webapp version — try both.
export const selectPluginState = (state: any): PluginState => {
    const slice =
        state?.[`plugins-${PLUGIN_ID}`] ??
        state?.plugins?.[PLUGIN_ID] ??
        state?.plugins?.plugins?.[PLUGIN_ID];
    return slice ?? initialState;
};
