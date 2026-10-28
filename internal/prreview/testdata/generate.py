"""Regenerate parity fixtures from a separately checked out pinned DK source.

Usage: python3 generate.py /path/to/dk-workflows
No network access or provider invocation. Not part of ordinary verification.
"""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys

root = Path(sys.argv[1]).resolve()
pin = '1b0019767a29142bbb4b3855c2fc9f1e4af75b71'
assert subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip() == pin
sys.path.insert(0, str(root/'runtime'))
def module(name, path):
    spec = importlib.util.spec_from_file_location(name, root/path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value
validator = module('validator', 'runtime/dk-pr-review/scripts/validate_result.py')
posting = module('posting', 'runtime/dk-pr-post-review/scripts/post_review.py')
decisions = module('decisions', 'runtime/dk-pr-decisions/scripts/decisions.py')
prep = module('prep', 'runtime/dk-pr-decisions/scripts/preparation.py')
patch = 'diff --git a/a.go b/a.go\nindex 123..456 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,4 @@\n context\n-old\n+new\n+next\n tail\n'
cases = [
    {'findings': []},
    {'findings': [{'severity':'Major','path':'a.go','line':2,'message':'Defect and consequence','fix':'Required outcome'}]},
    {'status':'tokens_exhausted','findings':[],'message':'usage limit'},
    {'status':'incomplete','findings':[],'message':'missing evidence'},
    {'findings': [{'severity':'Minor','path':'a.go','line':1,'message':'context','fix':'bad anchor'}]},
    {'findings': [{'severity':'P1','path':'a.go','line':2,'message':'wrong severity','fix':'fix'}]},
    {'findings': [], 'authorized': True},
    {'findings': None},
    {'status':'incomplete','findings':[],'message':' '},
]
responses=[]
for case in cases:
    try:
        validator.validate_response(case, validator.added_lines(patch))
        valid=True
    except ValueError:
        valid=False
    responses.append({'response':case,'valid':valid})
hash_values=[{'b': ['α', '<tag>', 3], 'a': True}, {'findings': []}, 'a\n\"b', {'path':'src/日本語.go'}]
conclusion={'text':'Keep established behavior','agreement':'established','material':False,'rationale':'Published scope establishes this boundary','evidence':['pr/body']}
analysis={'boundaries':{k:[conclusion] for k in prep.DIMENSIONS},'placement':{k:{**conclusion,'locations':['a.go']} for k in ('production','tests','reuse','entrypoints','dependencies')}, 'test_strategy':[dict(behavior='Changed behavior',existing='Current assertions',missing='Boundary regression',level='unit',location='a_test.go',command='go test ./...',rationale='Direct state assertions',limitation='Fixture only',evidence=['pr/body'])], 'questions':[], 'reviewer_addenda':{'one':'Inspect correctness','two':'Inspect scope'}, 'worker_instructions':'Inspect pinned scope, rules, code and tests', 'completion_criteria':'Check every engineering dimension', 'sync_required':False, 'sync_reason':''}
prep.validate_analysis(analysis,{'pr/body':{}},['one','two'])
body='Scope prose.\n\n## Decisions\n\n- **Choice:** Keep ordering.\n- Bound retry depth. Source: https://github.com/o/r/issues/1\n\n## Tests\nIgnored prose\n'
entries=decisions.body_entries(body)
for item in entries:
    item.pop('raw')
    if not item['references']:item['references']=['https://github.com/o/r/pull/2#decisions']
value={'upstream':pin,'patch':patch,'responses':responses,'hashes':[{'value':v,'sha256':hashlib.sha256(json.dumps(v,sort_keys=True,ensure_ascii=False).encode()).hexdigest()} for v in hash_values], 'locations':[list(v) for v in sorted(posting.locations(patch))], 'decisions_body':body,'decisions':entries,'analysis':analysis,'readiness':prep.readiness(analysis)}
paths=['runtime/dk-pr-review/scripts/validate_result.py','runtime/dk-pr-review/scripts/review_context.py','runtime/dk-pr-review/scripts/review-result.schema.json','runtime/dk-pr-post-review/scripts/post_review.py','runtime/dk-pr-decisions/scripts/preparation.py','runtime/dk-pr-decisions/scripts/decisions.py','runtime/shared/review_sessions.py','runtime/shared/review_coverage.py','runtime/shared/engineering_identity.py']
value['sources']={p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in paths}
import ast
runner_ast=ast.parse((root/'runtime/dk-pr-review/scripts/run_reviewers.py').read_text())
handoff_node=next(n.value for n in ast.walk(runner_ast) if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='handoff' for t in n.targets) and isinstance(n.value,ast.Dict))
invalid_analyses=[]
for kind in ('location','question','resolution','sync'):
    candidate=copy.deepcopy(analysis)
    if kind=='location':candidate['placement']['production']['locations']=[' ']
    elif kind in ('question','resolution'):
        candidate['questions']=[dict(id='q',status='resolved',material=True,statements='Conflict',affected_work='Review scope',recommendation='Narrow',proposed_changes='Choose scope',options=[dict(choice='Narrow',benefits='Bounded',costs='Deferred'),dict(choice='Wide',benefits='Coverage',costs='Complexity')],resolution='Resolved by source evidence',evidence=['pr/body'])]
        if kind=='question':candidate['questions'][0]['statements']=' '
        else:candidate['questions'][0]['resolution']=' '
    else:candidate['sync_required']=True;candidate['sync_reason']=' '
    try:
        prep.validate_analysis(candidate,{'pr/body':{}},['one','two']);valid=True
    except ValueError:valid=False
    invalid_analyses.append({'analysis':candidate,'valid':valid})
value['analysis_responses']=[{'analysis':analysis,'valid':True}]+invalid_analyses
value['handoff_fields']=[ast.literal_eval(k) for k in handoff_node.keys]+['reviewer_addendum']
value['decision_instructions']=next(ast.literal_eval(v) for k,v in zip(handoff_node.keys,handoff_node.values) if ast.literal_eval(k)=='decision_instructions')
Path(__file__).with_name('parity.json').write_text(json.dumps(value,indent=2,ensure_ascii=False)+'\n')

# A portable engineering-identity vector. File paths are synthetic; only the
# declared prior-absence capture is read by the upstream identity implementation.
import base64
import tempfile
from shared import engineering_identity, preparation_graph
binding_list=[]
for name in ('one','two'):
    binding={'reviewer':{'id':name,'kind':'skill','skillId':name},'resources':{'SKILL.md':base64.b64encode(b'Review pinned evidence').decode()},'modes':{'SKILL.md':384},'entrypoint':'SKILL.md','identity':''}
    binding['identity']=hashlib.sha256(json.dumps(binding,sort_keys=True,ensure_ascii=False).encode()).hexdigest()
    binding_list.append(binding)
keys=['manifest','context','decisions','metadata','patch']+[k+'/0' for k in prep.EVIDENCE]
with tempfile.TemporaryDirectory() as directory:
    prior=Path(directory)/'prior.json'
    prior_content={'verified_absence':True,'reason':'No selected prior preparation'}
    prior.write_text(json.dumps(prior_content))
    sources={k:{'path':'/fixture/'+k+'.json','snapshot':'/fixture/'+k+'.json','sha256':'f'*64,'mode':'capture','origin_identity':k} for k in keys}
    sources['prior_preparation/0'].update(path=str(prior),snapshot=str(prior),sha256=hashlib.sha256(prior.read_bytes()).hexdigest())
    descriptors={b['reviewer']['id']:{'descriptor_version':1,'reviewer':b['reviewer']['id'],'complete':True,'missing':[],'effective_prompt':'Inspect all governing evidence','aspects':['correctness'],'sources':[{'path':'/fixture/SKILL.md','sha256':'f'*64}],'code_factory_binding':b} for b in binding_list}
    graph={}
    for index,location in enumerate(preparation_graph.conclusions(analysis)):
        graph['c-'+str(index)]={'location':location,'depends_on':[],'paths':[{'path':'.','kind':'subtree'}],'sources':sorted(sources),'decisions':[]}
    preparation_graph.mandatory(graph,sources)
    prepared={'preparation_version':3,'mode':'standalone','identity':{'pr_url':'https://github.com/o/r/pull/2','base_sha':'b'*40,'head_sha':'a'*40,'body_hash':'e'*64,'decisions_hash':'d'*64},'workflow_dir':'/fixture','workspace':'/fixture/checkout','recovery_manifest':{'path':'/fixture/manifest.json','sha256':'f'*64},'recovery_context':{'path':'/fixture/context.json','sha256':'f'*64},'sources':sources,'inputs':{'policy':prep.policy(),'tree':{'a.go':['100644','blob','c'*40]},'sources':{k:{'identity':k,'governing':'e'*64} for k in keys}},'graph':graph,'retired_ids':[],'reviewer_order':['one','two'],'reviewers':descriptors,'reviewer_paths':['/fixture/reviewer-one.json','/fixture/reviewer-two.json'],'code_factory_bindings':binding_list,'local_answers':[],'analysis':analysis,'readiness':'ready','parent':None,'refresh':{'mode':'initial'}}
    expected=engineering_identity.engineering_id(prepared)
    sources['prior_preparation/0'].update(path='/fixture/prior.json',snapshot='/fixture/prior.json')
    prepared['engineering_id']=expected
    prepared['preparation_id']=preparation_graph.digest(prepared)
    Path(__file__).with_name('preparation.json').write_text(json.dumps({'upstream':pin,'preparation':prepared,'prior':prior_content,'engineering_id':expected},indent=2)+'\n')
