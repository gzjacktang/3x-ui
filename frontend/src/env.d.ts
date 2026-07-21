/// <reference types="vite/client" />

interface Window {
  X_UI_BASE_PATH?: string;
  X_UI_CUR_VER?: string;
  X_UI_DB_TYPE?: string;
}

declare module 'persian-calendar-suite' {
  import type { ComponentType, ReactNode } from 'react';

  type DateInput = string | number | null;
  type OutputFormat = 'iso' | 'shamsi' | 'gregorian' | 'hijri' | 'timestamp';

  interface PersianDateTimePickerProps {
    value?: DateInput;
    onChange?: (value: number | string | null) => void;
    defaultValue?: string | number | 'now' | null;
    showTime?: boolean;
    minuteStep?: number;
    outputFormat?: OutputFormat;
    showFooter?: boolean;
    theme?: Record<string, unknown>;
    disabledHours?: number[];
    minDate?: string | Date | null;
    maxDate?: string | Date | null;
    enabledDates?: string[] | null;
    disabledDates?: string[] | null;
    disabledWeekDays?: number[];
    persianNumbers?: boolean;
    rtlCalendar?: boolean;
    placeholder?: string;
    disabled?: boolean;
    className?: string;
    children?: ReactNode;
  }

  export const PersianDateTimePicker: ComponentType<PersianDateTimePickerProps>;
  export const PersianCalendar: ComponentType<Record<string, unknown>>;
  export const PersianDateRangePicker: ComponentType<Record<string, unknown>>;
  export const PersianTimePicker: ComponentType<Record<string, unknown>>;
  export const PersianTimeline: ComponentType<Record<string, unknown>>;
}
