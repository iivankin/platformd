package analytics

import (
	"strings"

	"github.com/iivankin/platformd/internal/state"
)

func Script(tracker state.AnalyticsTracker) string {
	aidDomain := "''"
	if tracker.Mode != state.AnalyticsModeCookieless {
		aidDomain = jsString(CookieDomain(tracker.RootDomain))
	}
	consentDomain := jsString(CookieDomain(tracker.RootDomain))
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(scriptSource,
		"__MODE__", jsString(tracker.Mode)),
		"__AID_DOMAIN__", aidDomain),
		"__CONSENT_DOMAIN__", consentDomain)
}

func jsString(value string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range value {
		switch r {
		case '\\', '\'':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

const scriptSource = `(function(){
var MODE=__MODE__,AID_DOMAIN=__AID_DOMAIN__,CONSENT_DOMAIN=__CONSENT_DOMAIN__;
var AID='platformd_aid',SID='platformd_sid',CONSENT='platformd_consent';
function cookie(n){var m=document.cookie.match('(?:^|; )'+n+'=([^;]*)');return m?decodeURIComponent(m[1]):null}
function setCookie(n,v,age,domain){var c=n+'='+encodeURIComponent(v)+';path=/;SameSite=Lax;max-age='+age;if(location.protocol==='https:')c+=';Secure';if(domain)c+=';Domain='+domain;document.cookie=c}
function uuid(){var a=crypto.getRandomValues(new Uint8Array(16));a[6]=a[6]&15|64;a[8]=a[8]&63|128;var h=[...a].map(function(b){return b.toString(16).padStart(2,'0')}).join('');return h.slice(0,8)+'-'+h.slice(8,12)+'-'+h.slice(12,16)+'-'+h.slice(16,20)+'-'+h.slice(20)}
function gpc(){return navigator.globalPrivacyControl===true}
function denied(){return gpc()||cookie(CONSENT)==='denied'}
function granted(){return cookie(CONSENT)==='granted'}
function allowed(){if(denied())return false;if(MODE==='opt-in')return granted();return true}
function anonymousId(){if(MODE==='cookieless'||denied())return null;if(MODE==='opt-in'&&!granted())return null;var id=cookie(AID);if(id)return id;id=uuid();setCookie(AID,id,31536000,AID_DOMAIN);return id}
function sessionId(){if(MODE==='cookieless'||!allowed())return null;var id=cookie(SID);if(id){setCookie(SID,id,1800,AID_DOMAIN);return id}id=uuid();setCookie(SID,id,1800,AID_DOMAIN);return id}
function send(name,props){if(!allowed())return;var body={n:name,u:location.href,t:document.title,r:document.referrer,w:screen.width+'x'+screen.height,l:navigator.language,p:props||{},i:!!props&&props.interactive===true,s:sessionId()};var aid=anonymousId();if(MODE!=='cookieless'&&!aid)return;var blob=new Blob([JSON.stringify(body)],{type:'application/json'});if(navigator.sendBeacon&&navigator.sendBeacon('/analytics/e',blob))return;fetch('/analytics/e',{method:'POST',body:blob,keepalive:true,credentials:'same-origin'}).catch(function(){})}
function track(name,props){send(name,props||{})}
function consent(value){if(value!=='granted'&&value!=='denied')return;setCookie(CONSENT,value,31536000,CONSENT_DOMAIN);if(value==='denied'){setCookie(AID,'',0,AID_DOMAIN);setCookie(SID,'',0,AID_DOMAIN)}window.dispatchEvent(new Event('platformd:consent'));if(value==='granted')pageview()}
function openFeatureHook(){return{after:function(ctx,details){if(!details||details.errorCode)return;if(details.reason&&details.reason!=='TARGETING_MATCH')return;track('$flag_called',{flag:ctx&&ctx.flagKey,variant:details.variant})}}}
function heatmapClick(e){var root=document.documentElement;var w=Math.max(root.scrollWidth,1);var h=Math.max(root.scrollHeight,1);track('$heatmap',{x:Math.max(0,Math.min(100,Math.round(e.pageX/w*100))),y:Math.max(0,Math.min(100,Math.round(e.pageY/h*100))),viewport_w:window.innerWidth,viewport_h:window.innerHeight,page_h:h,event_type:'click'})}
var lastScroll=0;
function heatmapScroll(){var now=Date.now();if(now-lastScroll<800)return;lastScroll=now;var root=document.documentElement;var span=Math.max(root.scrollHeight-window.innerHeight,1);var pct=Math.max(0,Math.min(100,Math.round(window.scrollY/span*100)));track('$heatmap',{x:50,y:pct,viewport_w:window.innerWidth,viewport_h:window.innerHeight,page_h:root.scrollHeight,scroll_pct:pct,event_type:'scroll'})}
var last=location.pathname+location.search;
function pageview(){if(document.visibilityState==='hidden')return;track('$pageview',{interactive:true})}
function pageleave(){track('$pageleave',{})}
function wrapHistory(method){var orig=history[method];history[method]=function(){orig.apply(this,arguments);if(last!==location.pathname+location.search){last=location.pathname+location.search;pageview()}}}
pageview();
wrapHistory('pushState');wrapHistory('replaceState');
window.addEventListener('popstate',function(){if(last!==location.pathname+location.search){last=location.pathname+location.search;pageview()}});
window.addEventListener('pagehide',pageleave);
document.addEventListener('click',heatmapClick,true);
window.addEventListener('scroll',heatmapScroll,{passive:true});
window.platformd={track:track,anonymousId:anonymousId,consent:consent,openFeatureHook:openFeatureHook};
})();`
