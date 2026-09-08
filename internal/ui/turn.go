package ui

import tea "charm.land/bubbletea/v2"

func (m *Model) finishAbort() tea.Cmd {
	if m.canceling && m.abortAcknowledged && m.abortBarrierSeen {
		m.canceling = false
		m.abortAcknowledged, m.abortBarrierSeen = false, false
		m.abortSessionID = ""
		m.experience.aborted = true
		if m.needsResume {
			m.report("Cancellation confirmed. Use /resume or /login to reconnect; your draft and completed edits are kept.", false)
		} else {
			m.report("Cancelled. Your draft is kept; completed edits were NOT undone. Nothing will be replayed.", false)
		}
		return m.startMoment(reactionCancelled)
	}
	return nil
}

func (m *Model) sendResult(msg sentMsg) tea.Cmd {
	if msg.generation != m.engineGeneration || msg.session != m.sessionGeneration || msg.turn != m.turnSequence || m.quitting {
		return nil
	}
	m.sendPending = false
	var cmd tea.Cmd
	if msg.err != nil {
		// A failed acknowledgement does not prove that the prompt or its tools
		// never ran. Keep the turn gated until the engine reaches idle or the
		// user explicitly aborts; an observed idle also wins over a late error.
		finished := msg.turn != 0 && m.lastIdleTurn == msg.turn
		m.needsAbort = !finished && m.lease != nil && !m.needsResume
		m.turn = m.needsAbort
		if !finished && m.composer.Value() == "" {
			m.composer.SetValue(msg.draft)
		}
		if m.localUser != nil {
			m.localUser.state = "delivery uncertain"
			if finished {
				m.localUser.state = "acknowledgement failed"
			}
			m.localUser.touch()
		}
		advice := "/login reconnects; no prompt was automatically retried."
		if m.needsAbort {
			advice = "Ctrl+C stops the unresolved turn before another send. Nothing was retried; completed edits are kept."
		} else if finished {
			advice = "The turn already ended. No prompt was automatically retried."
		}
		m.report(m.recoveryCopy("Send could not be confirmed: "+msg.err.Error()+". "+advice), true)
		m.renderDirty = true
		m.flushTimeline()
		cmd = tea.Batch(m.cancelDecisions(), m.startMoment(reactionRecovery))
	} else if msg.turn != 0 && m.lastIdleTurn == msg.turn {
		cmd = m.celebrateValidation()
	}
	if !m.turn && m.turnCancel != nil {
		m.turnCancel()
		m.turnCancel = nil
	}
	return cmd
}
