import { Menu, ArrowUpRight } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Sheet, SheetTrigger, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetClose } from '@/components/ui/sheet';

export default function MobileMenu({ home, download, docs }: { home: string; download: string; docs: string }) {
  const [ready, setReady] = useState(false);
  useEffect(() => setReady(true), []);
  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button variant="ghost" size="icon-lg" className="mobile-menu-button" aria-label="Open navigation" disabled={!ready}>
          <Menu aria-hidden="true" />
        </Button>
      </SheetTrigger>
      <SheetContent className="soda-sheet">
        <SheetHeader>
          <SheetTitle>Where to?</SheetTitle>
          <SheetDescription>Get Sodapop, browse the docs, or peek at the source.</SheetDescription>
        </SheetHeader>
        <nav aria-label="Mobile navigation" className="sheet-links">
          <SheetClose asChild><a href={`${home}#features`}>The good stuff</a></SheetClose>
          <SheetClose asChild><a href={download}>Get Sodapop <ArrowUpRight aria-hidden="true" /></a></SheetClose>
          <SheetClose asChild><a href={docs}>Documentation</a></SheetClose>
          <SheetClose asChild><a href="https://github.com/VeVarunSharma/sodapop">Source on GitHub</a></SheetClose>
        </nav>
      </SheetContent>
    </Sheet>
  );
}
