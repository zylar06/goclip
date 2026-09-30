import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import DraftPlayer from '../src/features/studio/DraftPlayer'
import { draftError, newDraft } from '../src/features/studio/types'
const scenes = [
  { id: 'one', label: '', evidence: '', start: 10, end: 12 },
  { id: 'two', label: '', evidence: '', start: 30, end: 33 },
  { id: 'three', label: '', evidence: '', start: 10, end: 11 },
]
describe('continuous draft player', () => {
  it('plays all ranges in edit order, stops at the final end and replays from the start', () => {
    render(<DraftPlayer src="/source" scenes={scenes} />)
    const video = screen.getByLabelText('Full draft playback') as HTMLVideoElement
    fireEvent.loadedMetadata(video)
    expect(video.currentTime).toBe(10)
    video.currentTime = 12; fireEvent.timeUpdate(video)
    expect(video.currentTime).toBe(30)
    video.currentTime = 33; fireEvent.timeUpdate(video)
    expect(video.currentTime).toBe(10)
    video.currentTime = 11; fireEvent.timeUpdate(video)
    expect(video.pause).toHaveBeenCalled()
    expect(video.currentTime).toBe(11)
    fireEvent.play(video)
    expect(video.currentTime).toBe(10)
  })
  it('allows unrestricted original-source selection and resets full playback after switching', () => {
    const onPoint = vi.fn()
    const { rerender } = render(<DraftPlayer src="/source" scenes={scenes} sourceMode selected={1} onPoint={onPoint} />)
    const video = screen.getByLabelText('Source playback') as HTMLVideoElement
    video.currentTime = 50; fireEvent.timeUpdate(video)
    fireEvent.click(screen.getByText('Use current source time'))
    expect(onPoint).toHaveBeenCalledWith(50)
    rerender(<DraftPlayer src="/source" scenes={scenes} />)
    expect(video.currentTime).toBe(10)
  })
  it('seeks the assembled draft timeline rather than the full source and limits opening artwork', () => {
    render(<DraftPlayer src="/source" scenes={scenes}><span>Opening artwork</span></DraftPlayer>)
    const video = screen.getByLabelText('Full draft playback') as HTMLVideoElement
    expect(screen.getByText('Opening artwork')).toBeInTheDocument()
    fireEvent.change(screen.getByRole('slider'), { target: { value: '3' } })
    expect(video.currentTime).toBe(31)
    expect(screen.queryByText('Opening artwork')).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('slider'), { target: { value: '5.5' } })
    expect(video.currentTime).toBe(10.5)
  })
  it('defaults new subtitle layers off and permits only 1ms beyond the source', () => {
    const draft = newDraft('Draft', [scenes[0]])
    expect(draft.subtitles).toBe(false)
    expect(draftError(draft, 11.999)).toBeNull()
    expect(draftError(draft, 11.998)).toBe('Scene exceeds source duration.')
  })
  it('clears codec warnings only when replacement media actually loads', () => {
    const { rerender } = render(<DraftPlayer src="/source" scenes={scenes} />)
    const video = screen.getByLabelText('Full draft playback')
    fireEvent.error(video)
    expect(screen.getByRole('alert')).toHaveTextContent('Playback unavailable')
    rerender(<DraftPlayer src="/compatible" scenes={scenes} />)
    expect(screen.getByRole('alert')).toBeInTheDocument()
    fireEvent.loadedData(video)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
