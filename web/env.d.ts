/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

declare module '@silverwind/vue3-calendar-heatmap' {
  import type { DefineComponent } from 'vue'
  const CalendarHeatmap: DefineComponent<{
    values: { date: string; count: number }[]
    until?: string
    darkMode?: boolean
    locale?: string
    tooltip?: boolean
    tooltipUnit?: string
  }, {}, any>
  export { CalendarHeatmap }
}
