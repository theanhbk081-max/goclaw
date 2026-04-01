package browser

import (
	"github.com/go-rod/rod/lib/launcher"
)

// StealthFlags returns Chrome launch flags that reduce automation detection.
// These should be applied to the launcher before Launch().
func StealthFlags(l *launcher.Launcher) {
	// CRITICAL: Rod's default launcher includes "enable-automation" flag which
	// causes Chrome to set navigator.webdriver=true and exposes automation signals.
	// Must delete this before anything else.
	l.Delete("enable-automation")

	// Remove "Chrome is being controlled by automated test software" bar
	l.Set("disable-blink-features", "AutomationControlled")

	// Disable infobars (automation notice)
	l.Set("disable-infobars")

	// Exclude CDP leak signals via Chrome feature flags.
	// AutomationControlled: removes navigator.webdriver at Chrome level.
	// TranslateUI: hides automation-specific UI elements.
	// EnableAutomation: additional automation detection surface.
	l.Set("disable-features", "AutomationControlled,TranslateUI,EnableAutomation")

	// Exclude "cdc_" prefix variables that automation tools inject.
	// Chrome DevTools Protocol leaves $cdc_ prefixed variables in the DOM.
	l.Set("enable-features", "NetworkService,NetworkServiceInProcess")

	// Standard fingerprint-normalizing flags
	l.Set("disable-background-networking")
	l.Set("disable-client-side-phishing-detection")
	l.Set("disable-default-apps")
	l.Set("disable-hang-monitor")
	l.Set("disable-popup-blocking")
	l.Set("disable-prompt-on-repost")
	l.Set("disable-sync")
	l.Set("metrics-recording-only")
	l.Set("no-first-run")
	l.Set("password-store", "basic")
	l.Set("use-mock-keychain")
	l.Set("export-tagged-pdf")

	// Disable automation extension that leaks signals
	l.Set("disable-extensions")
	l.Set("disable-component-extensions-with-background-pages")

	// Prevent WebRTC from leaking real IP behind proxy
	l.Set("enforce-webrtc-ip-permission-check")
	l.Set("disable-webrtc-hw-decoding")
	l.Set("disable-webrtc-hw-encoding")

	// Window size — headless default is 800x600 which is a dead giveaway
	l.Set("window-size", "1920,1080")
}

// StealthJS is JavaScript to inject on every new page to remove automation traces.
// Call page.Eval(StealthJS) after page creation.
// NOTE: For new pages, prefer stealthOnNewDocumentJS with EvalOnNewDocument
// which runs BEFORE any page JS (critical for defeating navigator.webdriver checks).
const StealthJS = `() => {
	// Remove navigator.webdriver
	Object.defineProperty(navigator, 'webdriver', {
		get: () => undefined,
		configurable: true,
	});

	// Fix chrome.runtime to look real
	if (!window.chrome) {
		window.chrome = {};
	}
	if (!window.chrome.runtime) {
		window.chrome.runtime = {
			connect: function() {},
			sendMessage: function() {},
		};
	}

	// Fix permissions query for notifications
	const originalQuery = window.navigator.permissions.query;
	window.navigator.permissions.query = (parameters) => (
		parameters.name === 'notifications' ?
			Promise.resolve({ state: Notification.permission }) :
			originalQuery(parameters)
	);

	// Fix plugins to look like a real browser (Chrome has at least 3)
	Object.defineProperty(navigator, 'plugins', {
		get: () => {
			const plugins = [
				{ name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
				{ name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: '' },
				{ name: 'Native Client', filename: 'internal-nacl-plugin', description: '' },
			];
			plugins.length = 3;
			return plugins;
		},
	});

	// Fix languages (should match fingerprint if set)
	Object.defineProperty(navigator, 'languages', {
		get: () => ['en-US', 'en'],
		configurable: true,
	});
}`

// stealthOnNewDocumentJS is raw JS (no IIFE wrapper) for use with
// Page.addScriptToEvaluateOnNewDocument via rod's EvalOnNewDocument.
// This runs BEFORE any page JavaScript, which is critical for defeating
// Google/Cloudflare bot detection that reads navigator.webdriver early.
const stealthOnNewDocumentJS = `
// --- navigator.webdriver ---
Object.defineProperty(navigator, 'webdriver', {
	get: () => undefined,
	configurable: true,
});

// --- chrome object ---
if (!window.chrome) {
	window.chrome = {};
}
if (!window.chrome.runtime) {
	window.chrome.runtime = {
		connect: function() {},
		sendMessage: function() {},
	};
}

// --- chrome.csi + chrome.loadTimes (missing = bot signal) ---
if (!window.chrome.csi) {
	window.chrome.csi = function() { return {}; };
}
if (!window.chrome.loadTimes) {
	window.chrome.loadTimes = function() { return {}; };
}

// --- Permissions API: resolve cleanly instead of error ---
if (window.navigator && window.navigator.permissions) {
	const origQuery = window.navigator.permissions.query;
	window.navigator.permissions.query = (params) => (
		params.name === 'notifications'
			? Promise.resolve({ state: Notification.permission })
			: origQuery(params)
	);
}

// --- Plugins (Chrome has at least 3) ---
Object.defineProperty(navigator, 'plugins', {
	get: () => {
		const plugins = [
			{ name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer', description: 'Portable Document Format' },
			{ name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai', description: '' },
			{ name: 'Native Client', filename: 'internal-nacl-plugin', description: '' },
		];
		plugins.length = 3;
		return plugins;
	},
});

// --- Languages (overridden by fingerprint if set) ---
Object.defineProperty(navigator, 'languages', {
	get: () => ['en-US', 'en'],
	configurable: true,
});

// --- Function.prototype.toString: hide CDP sourceURL leak ---
// CDP-injected scripts have "//# sourceURL=..." comments that detection scripts check.
const _origToString = Function.prototype.toString;
Function.prototype.toString = function() {
	if (this === Function.prototype.toString) return 'function toString() { [native code] }';
	return _origToString.call(this).replace(/\/\/# sourceURL=.*$/gm, '');
};

// --- navigator.connection (missing in headless) ---
if (!navigator.connection) {
	Object.defineProperty(navigator, 'connection', {
		get: () => ({
			effectiveType: '4g',
			rtt: 50,
			downlink: 10,
			saveData: false,
		}),
	});
}

// --- iframe contentWindow.chrome detection ---
// Ensure chrome object exists in dynamically created iframes.
const _origCreateElement = document.createElement;
document.createElement = function(...args) {
	const el = _origCreateElement.apply(this, args);
	if (args[0] && args[0].toLowerCase() === 'iframe') {
		el.addEventListener('load', () => {
			try {
				if (el.contentWindow && !el.contentWindow.chrome) {
					el.contentWindow.chrome = window.chrome;
				}
			} catch(e) { /* cross-origin, ignore */ }
		});
	}
	return el;
};

// --- CDP Runtime.enable detection bypass ---
// When CDP calls Runtime.enable, Chrome changes how Error objects are serialized
// via console.log (getter on 'stack' fires during serialization).
// Detection scripts exploit this to detect CDP-controlled browsers.
// We neutralize by preventing Error stack getter abuse for detection.
const _origError = Error;
const _origCaptureStackTrace = Error.captureStackTrace;
if (_origCaptureStackTrace) {
	Error.captureStackTrace = function(targetObject, constructorOpt) {
		_origCaptureStackTrace.call(Error, targetObject, constructorOpt);
		const origDescriptor = Object.getOwnPropertyDescriptor(targetObject, 'stack');
		if (origDescriptor && origDescriptor.get) {
			const origGet = origDescriptor.get;
			Object.defineProperty(targetObject, 'stack', {
				get: function() {
					return origGet.call(this);
				},
				set: origDescriptor.set,
				configurable: true,
			});
		}
	};
}

// --- Web Worker fingerprint consistency ---
// Override Worker constructor to inject stealth into workers too.
// Detection scripts compare main window vs Worker fingerprints.
const _origWorker = window.Worker;
if (_origWorker) {
	window.Worker = function(scriptURL, options) {
		return new _origWorker(scriptURL, options);
	};
	window.Worker.prototype = _origWorker.prototype;
	Object.defineProperty(window.Worker, 'name', { value: 'Worker' });
}

// --- navigator.mimeTypes (headless may have empty) ---
if (navigator.mimeTypes && navigator.mimeTypes.length === 0) {
	Object.defineProperty(navigator, 'mimeTypes', {
		get: () => {
			const mimes = [
				{ type: 'application/pdf', suffixes: 'pdf', description: 'Portable Document Format' },
				{ type: 'text/pdf', suffixes: 'pdf', description: '' },
			];
			mimes.length = 2;
			return mimes;
		},
	});
}

// --- Broken image dimensions fix ---
// In headless Chrome, broken images return 0x0. Real Chrome returns 16x16.
// Detection scripts load a broken image and check naturalWidth/naturalHeight.
const _origImage = window.Image;
window.Image = function(w, h) {
	const img = new _origImage(w, h);
	// Only override if the image fails to load (broken image detection)
	img.addEventListener('error', () => {
		if (img.naturalWidth === 0 && img.naturalHeight === 0) {
			Object.defineProperty(img, 'naturalWidth', { get: () => 16 });
			Object.defineProperty(img, 'naturalHeight', { get: () => 16 });
		}
	});
	return img;
};
window.Image.prototype = _origImage.prototype;
Object.defineProperty(window.Image, 'name', { value: 'Image' });
`
