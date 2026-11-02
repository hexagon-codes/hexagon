package browserruntime

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"
)

func TestIdentityHistoryKeepsClosedPagesWithCompactStorage(t *testing.T) {
	node, e := exec.LookPath("node")
	if e != nil {
		t.Skip("Node.js is unavailable for the JavaScript component test")
	}
	code := `const observe=` + observePageCode + `;
(async()=>{
 const all=[]; const context={pages:()=>all.filter(p=>!p.isClosed())};
 const create=()=>{let closed=false;let onClose;const p={context:()=>context,isClosed:()=>closed,url:()=>"about:blank",title:async()=>"",viewportSize:()=>({width:100,height:100}),once:(event,fn)=>{if(event==="close")onClose=fn},close:async()=>{closed=true;if(onClose)onClose()}};all.push(p);return p};
 const anchor=create();await observe(anchor);const ids=[];
 for(let i=0;i<4;i++){const p=create();const o=await observe(p);ids.push(o.hexclaw_browser_observation.page.id);await p.close()}
 let registry=context[Symbol.for('hexclaw.browser.identity')];
 const result={closed:registry.closed.size,ranges:registry.closed.ranges.length,live:context.pages().length,allKnown:ids.every(id=>registry.closed.has(id))};
 registry.closed=new Set(ids);await observe(anchor);result.migratedRanges=registry.closed.ranges.length;result.migratedKnown=ids.every(id=>registry.closed.has(id));
 const a=create(),b=create();await observe(a);await observe(b);await b.close();await a.close();result.outOfOrderRanges=registry.closed.ranges.length;await anchor.close();result.finalRanges=registry.closed.ranges.length;result.finalCount=registry.closed.size;
 console.log(JSON.stringify(result));
})().catch(e=>{console.error(e);process.exitCode=1});`
	ctx, c := context.WithTimeout(t.Context(), 3*time.Second)
	defer c()
	output, e := exec.CommandContext(ctx, node, "-e", code).CombinedOutput()
	if e != nil {
		t.Fatalf("JS component failed: %v %s", e, output)
	}
	var r struct {
		Closed, Ranges, Live, MigratedRanges, OutOfOrderRanges, FinalRanges, FinalCount int
		AllKnown, MigratedKnown                                                         bool
	}
	if e = json.Unmarshal(output, &r); e != nil {
		t.Fatal(e)
	}
	if r.Closed != 4 || r.Ranges != 1 || r.Live != 1 || !r.AllKnown || r.MigratedRanges != 1 || !r.MigratedKnown || r.OutOfOrderRanges != 1 || r.FinalRanges != 0 || r.FinalCount != 7 {
		t.Fatalf("identity history=%s", output)
	}
}
