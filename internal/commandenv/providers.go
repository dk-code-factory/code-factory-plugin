// Package commandenv preserves selected provider executables without changing
// the owner's global PATH or replacing installed programs.
package commandenv

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ChildOptions struct {
	Registry, RegistrySHA256 string
	ExternallyReadOnly       bool
	AuditDir                 string
	ContextHome              string
}

// Prepare writes small launchers so a common directory such as /usr/bin cannot
// shadow a selected gh or Codex executable from another directory.
func Prepare(directory string, providers map[string]string) (string, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) == "/" {
		return "", fmt.Errorf("invalid provider command directory")
	}
	tools := []string{"git", "gh", "codex", "systemctl"}
	hash := sha256.New()
	for _, tool := range tools {
		target := providers[tool]
		if !filepath.IsAbs(target) {
			return "", fmt.Errorf("selected %s executable must be absolute", tool)
		}
		info, err := os.Stat(target)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return "", fmt.Errorf("selected %s executable is unavailable", tool)
		}
		fmt.Fprintf(hash, "%s\x00%s\x00", tool, target)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	name := fmt.Sprintf("%x", hash.Sum(nil)[:16])
	if err := root.MkdirAll(name, 0700); err != nil {
		return "", err
	}
	bin, err := root.OpenRoot(name)
	if err != nil {
		return "", err
	}
	defer bin.Close()
	for _, tool := range []string{"git", "gh", "codex", "systemctl"} {
		target := providers[tool]
		if !filepath.IsAbs(target) {
			return "", fmt.Errorf("selected %s executable must be absolute", tool)
		}
		info, err := os.Stat(target)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return "", fmt.Errorf("selected %s executable is unavailable", tool)
		}
		quoted := "'" + strings.ReplaceAll(target, "'", "'\\''") + "'"
		script := []byte("#!/bin/sh\nexec " + quoted + " \"$@\"\n")
		if err := bin.WriteFile(tool, script, 0700); err != nil {
			return "", err
		}
	}
	return filepath.Join(directory, name), nil
}

func Path(directory, inherited string) string {
	if directory == "" {
		return inherited
	}
	if inherited == "" {
		return directory
	}
	return directory + string(os.PathListSeparator) + inherited
}

// PrepareCodex confines command-launched child providers to the task's explicit
// configuration. It keeps the selected executable and owner configuration intact.
func PrepareCodex(scratch, binary string, overrides []string, options ...ChildOptions) (string, error) {
	if !filepath.IsAbs(scratch) || filepath.Clean(scratch) == "/" || !filepath.IsAbs(binary) {
		return "", fmt.Errorf("invalid child provider paths")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("selected child provider is unavailable")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexec " + quote(binary)
	for _, override := range overrides {
		script += " -c " + quote(override)
	}
	script += " \"$@\"\n"
	if len(options) > 1 {
		return "", fmt.Errorf("invalid child provider options")
	}
	if len(options) == 1 {
		option := options[0]
		if option.Registry != "" && (!filepath.IsAbs(option.Registry) || len(option.RegistrySHA256) != 64) {
			return "", fmt.Errorf("invalid child provider registry snapshot")
		}
		if option.AuditDir != "" && !filepath.IsAbs(option.AuditDir) {
			return "", fmt.Errorf("invalid child provider audit directory")
		}
		if option.ContextHome != "" && !filepath.IsAbs(option.ContextHome) {
			return "", fmt.Errorf("invalid native child context directory")
		}
		config, err := json.Marshal(map[string]any{"binary": binary, "overrides": overrides, "registry": option.Registry, "digest": option.RegistrySHA256, "externalReadOnly": option.ExternallyReadOnly, "auditDir": option.AuditDir, "contextHome": option.ContextHome})
		if err != nil {
			return "", err
		}
		quoted, _ := json.Marshal(string(config))
		script = fmt.Sprintf(childProviderLauncher, quoted)
	}
	companion, err := CodexCompanion(binary)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(script))
	name := filepath.Join("provider-bin", fmt.Sprintf("%x", digest[:16]))
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return "", err
	}
	// Anchor above all task-writable child components. A retained task may have
	// replaced provider-bin with a link; resuming must never follow it outside.
	root, err := os.OpenRoot(scratch)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := root.MkdirAll(name, 0700); err != nil {
		return "", err
	}
	if err := root.WriteFile(filepath.Join(name, "codex"), []byte(script), 0700); err != nil {
		return "", err
	}
	if companion != "" {
		launcher := "#!/bin/sh\nexec " + quote(companion) + " \"$@\"\n"
		if err := root.WriteFile(filepath.Join(name, "codex-code-mode-host"), []byte(launcher), 0700); err != nil {
			return "", err
		}
	}
	return filepath.Join(scratch, name), nil
}

const childProviderLauncher = `#!/usr/bin/python3
import hashlib,json,os,re,stat,subprocess,sys,time
cfg=json.loads(%s)
args=sys.argv[1:]
if args==['plugin','list','--json'] and cfg['registry']:
 try:
  with open(cfg['registry'],'rb') as f: data=f.read(16*1024*1024+1)
  if len(data)>16*1024*1024 or hashlib.sha256(data).hexdigest()!=cfg['digest']:
   raise ValueError('registry integrity')
  registry=json.loads(data)
  if not isinstance(registry,dict) or not isinstance(registry.get('installed'),list):
   raise ValueError('registry shape')
 except (OSError,ValueError):
  sys.exit('Retained native plugin inventory unavailable or changed; no live fallback.')
 os.write(1,data)
 sys.exit(0)
# The SDK's noninteractive read-only invocation cannot nest a second bwrap
# sandbox. This launcher is selected only INSIDE the already enforced read-only
# task. Retain that outer filesystem/network boundary, rather than retrying a
# failed command outside it. Other invocations keep their original arguments.
supported=args[:6]==['exec','--ephemeral','--sandbox','read-only','--skip-git-repo-check','-C'] and len(args)>6 and os.path.isabs(args[6])
if cfg['externalReadOnly'] and supported:
 try:
  with open('/proc/self/status') as f:
   status=dict(line.split(':',1) for line in f if ':' in line)
  if status.get('NoNewPrivs','').strip()!='1' or status.get('Seccomp','').strip()!='2':
   raise ValueError('missing enforced process restrictions')
 except (OSError,ValueError):
  sys.exit('Nested review execution requires the enforced outer task sandbox.')
 args[3]='danger-full-access'
flags=[value for override in (cfg['overrides'] or []) for value in ['-c',override]]
native_environment=dict(os.environ)
if supported:
 native_environment['PATH']=os.path.dirname(os.path.abspath(__file__))+os.pathsep+native_environment.get('PATH','')
 flags+=['-c','shell_environment_policy.set.PATH='+json.dumps(native_environment['PATH'])]
if supported and cfg['contextHome']:
 # This is the child client's actual private Codex state directory. The parent
 # environment and existing authentication files are unchanged/read-only.
 native_environment['CODEX_HOME']=cfg['contextHome']
 args.pop(1)
if supported and cfg['auditDir']:
 os.makedirs(cfg['auditDir'],mode=0o700,exist_ok=True)
 path=os.path.join(cfg['auditDir'],str(time.time_ns())+'-'+str(os.getpid())+'.jsonl')
 with open(path,'x',opener=lambda p,f:os.open(p,f,0o600)) as audit:
  def emit(value):
   audit.write(json.dumps(value,separators=(',',':'))+'\n');audit.flush()
  emit({'event':'invocation','version':1,'externalReadOnly':cfg['externalReadOnly'],'registrySHA256':cfg['digest']})
  root_thread=''
  args.insert(1,'--json')
  process=subprocess.Popen([cfg['binary'],*flags,*args],stdout=subprocess.PIPE,env=native_environment)
  for raw in process.stdout:
   sys.stdout.buffer.write(raw);sys.stdout.buffer.flush()
   record={'event':'unparsed','sha256':hashlib.sha256(raw).hexdigest()}
   try:
    value=json.loads(raw);item=value.get('item',{})
    kind=value.get('type','unknown')
    record['event']=kind if isinstance(kind,str) and re.fullmatch(r'[A-Za-z0-9_.-]{1,80}',kind) else 'unknown'
    if isinstance(value.get('thread_id'),str) and re.fullmatch(r'[A-Za-z0-9_-]{1,160}',value['thread_id']):
     record['thread_id']=value['thread_id']
     if value.get('type')=='thread.started' and not root_thread:root_thread=value['thread_id']
    if isinstance(item,dict):
     record['item_keys']=sorted(item)
     if item.get('type')=='error':
      detail=json.dumps(item).lower()
      record['errorCategories']=[name for name,pattern in [('unsupported_item','unsupported'),('model_context','model context'),('permission','permission denied'),('missing_file','no such file'),('network_policy','not on the allowlist'),('provider_limit','usage limit')] if pattern in detail]
     for key in ['id','type','status','tool','tool_name','name','sender_thread_id','receiver_thread_ids','senderThreadId','receiverThreadIds']:
      v=item.get(key)
      if isinstance(v,str) and re.fullmatch(r'[A-Za-z0-9_.:-]{1,160}',v):record[key]=v
      elif key in ['receiver_thread_ids','receiverThreadIds'] and isinstance(v,list):
       record[key]=[x for x in v if isinstance(x,str) and re.fullmatch(r'[A-Za-z0-9_-]{1,160}',x)][:32]
     for key in ['agents_states','agent_states','agentsStates','agentStates']:
      if isinstance(item.get(key),dict):
       record[key]={k:{'status':v.get('status')} for k,v in item[key].items() if re.fullmatch(r'[A-Za-z0-9_-]{1,160}',k) and isinstance(v,dict) and v.get('status') in ['pending_init','running','completed','interrupted','errored','shutdown','not_found']}
     if isinstance(item.get('agents'),list):
      record['agents']=[{k:v for k,v in agent.items() if k in ['id','thread_id','status'] and isinstance(v,str) and re.fullmatch(r'[A-Za-z0-9_-]{1,160}',v)} for agent in item['agents'] if isinstance(agent,dict)][:32]
     if 'collab' in str(item.get('type','')).lower() or item.get('tool') in ['spawn_agent','wait','wait_agent']:
      text=json.dumps(item)
      record['roleHints']=[name for name in ['Toolkit','Standards','Spec'] if re.search(r'\b'+name+r'\b',text,re.I)]
   except (ValueError,TypeError,AttributeError):pass
   emit(record)
  code=process.wait()
  # V2 activity is omitted by this pinned CLI's JSON converter. Project only
  # IDs, completion and known role labels from this task's private session store.
  # Never traverse owner sessions or retain creator/account IDs or message bodies.
  if root_thread and cfg['contextHome']:
   candidates=[];nodes={};remaining=64*1024*1024
   for directory,dirs,files in os.walk(os.path.join(cfg['contextHome'],'sessions'),followlinks=False):
    for name in files:
     file=os.path.join(directory,name)
     try:
      info=os.lstat(file)
      if name.endswith('.jsonl') and stat.S_ISREG(info.st_mode) and info.st_size<=16*1024*1024:
       candidates.append((info.st_mtime_ns,file))
     except OSError:pass
   for _,file in sorted(candidates,reverse=True)[:256]:
    if remaining<=0:break
    try:
     fd=os.open(file,os.O_RDONLY|os.O_NOFOLLOW)
     with os.fdopen(fd,'rb') as source:data=source.read(min(remaining,16*1024*1024)+1)
     remaining-=len(data);node={'status':'unknown','reviewer':None}
     for line in data.splitlines():
      try:value=json.loads(line);payload=value.get('payload',{})
      except (ValueError,TypeError):continue
      if not isinstance(payload,dict):continue
      if value.get('type')=='session_meta' and 'id' not in node:
       for key in ['id','parent_thread_id']:
        v=payload.get(key)
        if isinstance(v,str) and re.fullmatch(r'[A-Za-z0-9_-]{1,160}',v):node[key]=v
       label=re.sub(r'([a-z])([A-Z])',r'\1 \2',str(payload.get('agent_path',''))).lower()
       words=set(re.findall(r'[a-z]+',label))
       node['roleHints']=[name for name,tokens in [('Toolkit',{'toolkit'}),('Standards',{'standards','standard'}),('Spec',{'spec','specification'})] if words.intersection(tokens)]
      elif value.get('type')=='event_msg':
       kind=payload.get('type')
       if kind in ['task_started','turn_started']:node['status']='running'
       elif kind in ['task_complete','turn_complete']:node['status']='completed'
       elif kind=='turn_aborted':node['status']='interrupted'
      elif value.get('type')=='response_item' and payload.get('type')=='message' and payload.get('role')=='user' and node['reviewer'] is None:
       content=payload.get('content',[])
       text='\n'.join(v.get('text','') for v in content if isinstance(v,dict)) if isinstance(content,list) else ''
       for name in ['code-review-ad','code-review-matpocock']:
        if re.search(r'Apply the installed procedure at [^\n]*/'+re.escape(name)+r'/SKILL\.md',text):node['reviewer']=name;break
     if node.get('id'):nodes.setdefault(node['id'],node)
    except (OSError,ValueError,TypeError,AttributeError):pass
   root=nodes.get(root_thread,{})
   emit({'event':'native.root','thread_id':root_thread,'reviewer':root.get('reviewer'),'status':root.get('status','unknown')})
   descendants={root_thread}
   for _ in range(16):
    found={key for key,value in nodes.items() if value.get('parent_thread_id') in descendants}
    if found.issubset(descendants):break
    descendants.update(found)
   for thread in sorted(descendants-{root_thread}):
    node=nodes[thread]
    emit({'event':'native.agent','thread_id':thread,'parent_thread_id':node['parent_thread_id'],'root_thread_id':root_thread,'status':node['status'],'roleHints':node.get('roleHints',[])})
  emit({'event':'exit','returncode':code})
 sys.exit(code)
os.execve(cfg['binary'],[cfg['binary'],*flags,*args],native_environment)
`

// CodexCompanion selects only the companion shipped beside the selected binary.
// Other installations on PATH cannot substitute their code execution host.
func CodexCompanion(binary string) (string, error) {
	path := filepath.Join(filepath.Dir(binary), "codex-code-mode-host")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("selected Codex companion is not a regular executable")
	}
	return path, nil
}
