import { describe, it, expect } from 'vitest'
import { titlePresets, titleVersions, isArtworkStyle } from '../src/features/studio/titlePresets'
import { draftError, newDraft } from '../src/features/studio/types'

describe('current Go title templates',()=>{
  it('never advertises historical engines that the Go renderer does not implement',()=>{
    for(const preset of titlePresets){
      expect(titleVersions(preset.value).map(v=>v.value)).toEqual([isArtworkStyle(preset.value)?6:1])
    }
  })
  it('rejects unsupported historical template input',()=>{
    const draft=newDraft('test',[{id:'s1',start:0,end:2,label:'',evidence:''}])
    expect(draftError({...draft,title_template_version:4})).toBeTruthy()
    expect(draftError(draft)).toBeNull()
  })
})
