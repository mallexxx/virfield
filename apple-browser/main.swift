import AppKit
import WebKit

final class AppleBrowser: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    private var window: NSWindow!
    private var webView: WKWebView!
    private var address: NSTextField!
    private var status: NSTextField!
    private var backButton: NSButton!
    private var token: String?
    private var origin: URL?
    private var downloadURL: URL?
    private var submitting = false
    private var pendingLinks: [URL] = []
    private var popupWindows: [ObjectIdentifier: NSWindow] = [:]

    func applicationDidFinishLaunching(_ notification: Notification) {
        installMainMenu()
        let visible = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1024, height: 768)
        let frame = NSRect(x: 0, y: 0, width: min(980, visible.width - 24),
                           height: min(760, visible.height - 48))
        window = NSWindow(contentRect: frame, styleMask: [.titled, .closable, .miniaturizable, .resizable],
                          backing: .buffered, defer: false)
        window.title = "Virfield — Apple Developer"
        window.delegate = self
        window.contentMinSize = NSSize(width: min(560, frame.width), height: min(420, frame.height))
        window.contentMaxSize = NSSize(width: visible.width - 24, height: visible.height - 48)
        window.center()

        let content = NSView(frame: frame)
        window.contentView = content

        address = NSTextField(labelWithString: "Waiting for Apple Developer link")
        address.lineBreakMode = .byTruncatingMiddle
        address.isSelectable = true
        address.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        status = NSTextField(labelWithString: "Open the one-time link from Virfield.")
        status.lineBreakMode = .byTruncatingTail
        status.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        backButton = NSButton(title: "Back", target: self, action: #selector(goBack))
        backButton.isEnabled = false
        let restartButton = NSButton(title: "Restart Sign-In", target: self, action: #selector(restartSignIn))
        let button = NSButton(title: "Continue Xcode download", target: self, action: #selector(continueDownload))
        let controls = NSStackView(views: [backButton, restartButton, address, button])
        controls.orientation = .horizontal
        controls.spacing = 12
        controls.distribution = .fill
        controls.translatesAutoresizingMaskIntoConstraints = false
        status.translatesAutoresizingMaskIntoConstraints = false
        address.setContentHuggingPriority(.defaultLow, for: .horizontal)
        button.setContentHuggingPriority(.required, for: .horizontal)

        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        webView = WKWebView(frame: .zero, configuration: configuration)
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(controls)
        content.addSubview(status)
        content.addSubview(webView)
        NSLayoutConstraint.activate([
            controls.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 12),
            controls.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -12),
            controls.topAnchor.constraint(equalTo: content.topAnchor, constant: 12),
            status.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 12),
            status.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -12),
            status.topAnchor.constraint(equalTo: controls.bottomAnchor, constant: 8),
            webView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            webView.topAnchor.constraint(equalTo: status.bottomAnchor, constant: 8),
            webView.bottomAnchor.constraint(equalTo: content.bottomAnchor)
        ])
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        for argument in ProcessInfo.processInfo.arguments.dropFirst() {
            if let url = URL(string: argument), url.scheme == "virfield-apple-auth" {
                openLink(url)
            }
        }
        for url in pendingLinks { openLink(url) }
        pendingLinks.removeAll()
    }

    private func installMainMenu() {
        let main = NSMenu()
        let appItem = NSMenuItem()
        let appMenu = NSMenu(title: "Virfield Apple Browser")
        appItem.submenu = appMenu
        appMenu.addItem(withTitle: "Quit Virfield Apple Browser",
                        action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        main.addItem(appItem)

        let editItem = NSMenuItem()
        let editMenu = NSMenu(title: "Edit")
        editItem.submenu = editMenu
        for (title, action, key) in [
            ("Cut", #selector(NSText.cut(_:)), "x"),
            ("Copy", #selector(NSText.copy(_:)), "c"),
            ("Paste", #selector(NSText.paste(_:)), "v"),
            ("Select All", #selector(NSText.selectAll(_:)), "a")
        ] {
            let item = editMenu.addItem(withTitle: title, action: action, keyEquivalent: key)
            item.keyEquivalentModifierMask = .command
        }
        main.addItem(editItem)

        let navigationItem = NSMenuItem()
        let navigationMenu = NSMenu(title: "Navigation")
        navigationItem.submenu = navigationMenu
        let back = navigationMenu.addItem(withTitle: "Back", action: #selector(goBack), keyEquivalent: "[")
        back.keyEquivalentModifierMask = .command
        let restart = navigationMenu.addItem(withTitle: "Restart Apple Sign-In", action: #selector(restartSignIn), keyEquivalent: "r")
        restart.keyEquivalentModifierMask = [.command, .shift]
        main.addItem(navigationItem)
        NSApp.mainMenu = main
    }

    func windowWillResize(_ sender: NSWindow, to frameSize: NSSize) -> NSSize {
        guard let visible = sender.screen?.visibleFrame else { return frameSize }
        return NSSize(width: min(frameSize.width, visible.width - 16),
                      height: min(frameSize.height, visible.height - 16))
    }

    func windowDidChangeScreen(_ notification: Notification) {
        guard let visible = window.screen?.visibleFrame else { return }
        window.contentMaxSize = NSSize(width: visible.width - 24, height: visible.height - 48)
        var frame = window.frame
        frame.size.width = min(frame.width, visible.width - 16)
        frame.size.height = min(frame.height, visible.height - 16)
        frame.origin.x = max(visible.minX, min(frame.origin.x, visible.maxX - frame.width))
        frame.origin.y = max(visible.minY, min(frame.origin.y, visible.maxY - frame.height))
        window.setFrame(frame, display: true)
    }

    func application(_ application: NSApplication, open urls: [URL]) {
        if window == nil {
            pendingLinks.append(contentsOf: urls)
        } else {
            for url in urls { openLink(url) }
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    private func trustedOrigin() -> URL? {
        let root = Bundle.main.bundleURL.deletingLastPathComponent().deletingLastPathComponent()
        let config = root.appendingPathComponent("config.json")
        guard let data = try? Data(contentsOf: config),
              let value = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              value["state_dir"] as? String == root.path,
              let listen = value["listen"] as? String,
              let origin = URL(string: "http://\(listen)"),
              origin.host == "127.0.0.1", origin.port != nil,
              origin.path.isEmpty || origin.path == "/" else { return nil }
        return origin
    }

    private func openLink(_ url: URL) {
        guard url.scheme == "virfield-apple-auth", url.host == "start",
              let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems,
              let value = query.first(where: { $0.name == "token" })?.value,
              value.count == 43,
              value.utf8.allSatisfy({ $0 == 45 || ($0 >= 48 && $0 <= 57) || ($0 >= 65 && $0 <= 90) || $0 == 95 || ($0 >= 97 && $0 <= 122) }),
              let originText = query.first(where: { $0.name == "origin" })?.value,
              let originURL = URL(string: originText), originURL.scheme == "http",
              originURL.host == "127.0.0.1", originURL.port != nil,
              originURL.path.isEmpty || originURL.path == "/",
              let trusted = trustedOrigin(), originURL == trusted,
              let downloadText = query.first(where: { $0.name == "download" })?.value,
              let downloadURL = URL(string: downloadText), downloadURL.scheme == "https",
              downloadURL.host == "developer.apple.com",
              downloadURL.path == "/services-account/download",
              let downloadPath = URLComponents(url: downloadURL, resolvingAgainstBaseURL: false)?
                  .queryItems?.first(where: { $0.name == "path" })?.value,
              downloadPath.hasPrefix("/Developer_Tools/"), downloadPath.hasSuffix(".xip") else {
            status?.stringValue = "Invalid Virfield Apple link."
            return
        }
        token = value
        origin = originURL
        self.downloadURL = downloadURL
        submitting = false
        status.stringValue = "Sign in on Apple's page. Download will continue in Virfield."
        webView.load(URLRequest(url: downloadURL))
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        if webView === self.webView {
            address.stringValue = webView.url?.absoluteString ?? "Apple Developer"
            backButton.isEnabled = webView.canGoBack
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if let url = navigationAction.request.url, url.path.lowercased().hasSuffix(".xip") {
            decisionHandler(.cancel)
            continueDownload()
        } else {
            decisionHandler(.allow)
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        let mime = navigationResponse.response.mimeType?.lowercased() ?? ""
        if mime.contains("xip") || mime.contains("octet-stream") {
            decisionHandler(.cancel)
            continueDownload()
        } else {
            decisionHandler(.allow)
        }
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
        download.cancel()
        continueDownload()
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
        download.cancel()
        continueDownload()
    }

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse,
                  suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        completionHandler(nil)
        continueDownload()
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        guard navigationAction.targetFrame == nil else { return nil }
        let popup = WKWebView(frame: .zero, configuration: configuration)
        popup.navigationDelegate = self
        popup.uiDelegate = self
        let visible = window.screen?.visibleFrame ?? NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 900, height: 700)
        let width = min(760, visible.width - 40)
        let height = min(720, visible.height - 60)
        let popupWindow = NSWindow(contentRect: NSRect(x: 0, y: 0, width: width, height: height),
                                   styleMask: [.titled, .closable, .miniaturizable, .resizable],
                                   backing: .buffered, defer: false)
        popupWindow.title = "Apple Account Verification"
        popupWindow.contentView = popup
        popupWindow.delegate = self
        popupWindow.center()
        popupWindows[ObjectIdentifier(popup)] = popupWindow
        popupWindow.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        return popup
    }

    func webViewDidClose(_ webView: WKWebView) {
        let key = ObjectIdentifier(webView)
        if let popupWindow = popupWindows.removeValue(forKey: key) {
            popupWindow.delegate = nil
            popupWindow.close()
        }
    }

    func windowWillClose(_ notification: Notification) {
        guard let closing = notification.object as? NSWindow, closing !== window else { return }
        if let key = popupWindows.first(where: { $0.value === closing })?.key {
            popupWindows.removeValue(forKey: key)
        }
    }

    @objc private func goBack() {
        guard webView.canGoBack else { return }
        webView.goBack()
        backButton.isEnabled = webView.canGoBack
    }

    @objc private func restartSignIn() {
        guard let downloadURL else { return }
        submitting = false
        let windows = Array(popupWindows.values)
        popupWindows.removeAll()
        for popup in windows {
            popup.delegate = nil
            popup.close()
        }
        status.stringValue = "Restarting Apple sign-in…"
        webView.configuration.websiteDataStore.removeData(
            ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(),
            modifiedSince: .distantPast
        ) { [weak self] in
            DispatchQueue.main.async {
                guard let self, self.downloadURL == downloadURL else { return }
                self.webView.load(URLRequest(url: downloadURL))
                self.status.stringValue = "Sign in on Apple's page. Download will continue in Virfield."
            }
        }
    }

    @objc private func continueDownload() {
        guard !submitting, let token = token, let origin = origin else { return }
        submitting = true
        status.stringValue = "Checking Apple download session…"
        webView.configuration.websiteDataStore.httpCookieStore.getAllCookies { [weak self] cookies in
            guard let self else { return }
            let allowed = Set(["apple.com", "developer.apple.com", "download.developer.apple.com"])
            let lines = cookies.compactMap { cookie -> String? in
                let host = cookie.domain.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "."))
                guard allowed.contains(host),
                      cookie.path == "/" || cookie.path.hasPrefix("/Developer_Tools/"),
                      ![cookie.domain, cookie.path, cookie.name, cookie.value].contains(where: {
                          $0.contains("\n") || $0.contains("\r") || $0.contains("\t")
                      }) else { return nil }
                let broad = cookie.domain.hasPrefix(".") || host == "apple.com"
                let expiry = Int64(cookie.expiresDate?.timeIntervalSince1970 ?? 0)
                return "\(cookie.domain)\t\(broad ? "TRUE" : "FALSE")\t\(cookie.path)\t\(cookie.isSecure ? "TRUE" : "FALSE")\t\(expiry)\t\(cookie.name)\t\(cookie.value)"
            }
            guard !lines.isEmpty else {
                DispatchQueue.main.async {
                    self.submitting = false
                    self.status.stringValue = "Apple sign-in has not completed. Finish it on Apple's page."
                }
                return
            }
            let endpoint = origin.appendingPathComponent("apple-auth/\(token)/session")
            var request = URLRequest(url: endpoint)
            request.httpMethod = "POST"
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try? JSONSerialization.data(withJSONObject: ["cookies": lines.joined(separator: "\n") + "\n"])
            URLSession(configuration: .ephemeral).dataTask(with: request) { _, response, error in
                DispatchQueue.main.async {
                    self.submitting = false
                    let statusCode = (response as? HTTPURLResponse)?.statusCode
                    if statusCode == 202 {
                        self.status.stringValue = "Virfield resumed the Xcode download."
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
                            if self.token == token { NSApp.terminate(nil) }
                        }
                    } else if statusCode == 404 {
                        self.status.stringValue = "This 15-minute link expired. Request a new Virfield Apple sign-in link."
                    } else if error != nil {
                        self.status.stringValue = "Virfield is unreachable. Keep this window open and retry."
                    } else {
                        self.status.stringValue = "Download session was not accepted. Finish sign-in on Apple's page, then retry."
                    }
                }
            }.resume()
        }
    }
}

let application = NSApplication.shared
let delegate = AppleBrowser()
application.delegate = delegate
application.setActivationPolicy(.regular)
application.run()
