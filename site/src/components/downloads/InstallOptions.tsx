import { useEffect, useState } from 'react';
import { Check, Copy, Terminal } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import type { InstallMethod } from '@/lib/install-methods.mjs';

function CopyCommand({ command }: { command: string }) {
  const [status, setStatus] = useState<'idle' | 'copied' | 'error'>('idle');
  const [ready, setReady] = useState(false);
  useEffect(() => setReady(true), []);
  async function copy() {
    if (!navigator.clipboard) {
      setStatus('error');
      return;
    }
    try {
      await navigator.clipboard.writeText(command);
      setStatus('copied');
    } catch (error) {
      if (!(error instanceof DOMException) && !(error instanceof Error)) throw error;
      setStatus('error');
    }
  }
  return (
    <>
      <div className="command-line">
        <span className="command-dollar" aria-hidden="true">$</span>
        <code tabIndex={0}>{command}</code>
        <Button variant="ghost" className="copy-command" aria-label="Copy installation command" disabled={!ready} onClick={copy}>
          {status === 'copied' ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
        </Button>
      </div>
      <p className="copy-status" role="status">
        {status === 'copied' ? 'Copied. Your terminal is next.' :
          status === 'error' ? 'Could not copy. Select the command above and copy it manually.' : '\u00a0'}
      </p>
    </>
  );
}

export default function InstallOptions({ methods }: { methods: [InstallMethod, ...InstallMethod[]] }) {
  return (
    <Tabs defaultValue={methods[0].id} className="install-options">
      <TabsList aria-label="Installation method" className="install-tabs">
        {methods.map((method) => <TabsTrigger key={method.id} value={method.id}><Terminal aria-hidden="true" />{method.label}</TabsTrigger>)}
      </TabsList>
      {methods.map((method) => (
        <TabsContent key={method.id} value={method.id}>
          <CopyCommand command={method.command} />
          <p className="install-description">{method.description}</p>
        </TabsContent>
      ))}
    </Tabs>
  );
}
