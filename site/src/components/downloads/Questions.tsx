import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui/accordion';

export default function Questions() {
  return (
    <Accordion type="single" collapsible className="questions">
      <AccordionItem value="copilot">
        <AccordionTrigger>Is this the official Copilot CLI?</AccordionTrigger>
        <AccordionContent>No. Sodapop is an independent terminal application built with the official GitHub Copilot SDK, with its own interface and GitHub OAuth onboarding.</AccordionContent>
      </AccordionItem>
      <AccordionItem value="account">
        <AccordionTrigger>What account do I need?</AccordionTrigger>
        <AccordionContent>An eligible GitHub account with Copilot access, subject to your organization's policies. GitHub sign-in and Copilot entitlement are separate steps; normal Copilot usage rules apply.</AccordionContent>
      </AccordionItem>
      <AccordionItem value="permissions">
        <AccordionTrigger>Am I still in control of changes?</AccordionTrigger>
        <AccordionContent>By default, structured reads inside your project may run automatically. Edits, shell commands, and external access need your approval. Planning is advisory, not read-only, and cancellation does not undo completed edits.</AccordionContent>
      </AccordionItem>
    </Accordion>
  );
}
