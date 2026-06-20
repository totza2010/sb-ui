/**
 * Settings — single home for all sb-ui configuration, split by mode:
 *   General (Plex/uploader options) · Proxy (Tailscale / tsdproxy).
 */
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Settings as SettingsIcon, SlidersHorizontal, Network, FileCog } from 'lucide-react'
import { OptionsPanel } from '@/pages/Options'
import { ProxyPanel } from '@/pages/Proxy'
import { ConfigPanel } from '@/pages/ConfigEditor'

export function Settings() {
  return (
    <div className="p-6 space-y-5">
      <h1 className="text-xl font-semibold text-foreground flex items-center gap-2"><SettingsIcon className="h-5 w-5" />Settings</h1>

      <Tabs defaultValue="general">
        <TabsList>
          <TabsTrigger value="general" className="gap-1.5"><SlidersHorizontal className="h-3.5 w-3.5" />General</TabsTrigger>
          <TabsTrigger value="config" className="gap-1.5"><FileCog className="h-3.5 w-3.5" />Config files</TabsTrigger>
          <TabsTrigger value="proxy" className="gap-1.5"><Network className="h-3.5 w-3.5" />Proxy (Tailscale)</TabsTrigger>
        </TabsList>

        <TabsContent value="general"><OptionsPanel /></TabsContent>
        <TabsContent value="config"><ConfigPanel /></TabsContent>
        <TabsContent value="proxy"><ProxyPanel /></TabsContent>
      </Tabs>
    </div>
  )
}
