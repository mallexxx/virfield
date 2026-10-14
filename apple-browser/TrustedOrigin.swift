import Foundation
import Darwin

// The browser trusts only the VM user's private deployment configuration.
// Controller state may live separately from the deployment and app bundle.
func trustedAppleOrigin(deployment: URL) -> URL? {
    let config = deployment.appendingPathComponent("config.json")
    guard privateOwnedItem(deployment, kind: mode_t(S_IFDIR)),
          privateOwnedItem(config, kind: mode_t(S_IFREG)),
          let data = try? Data(contentsOf: config), data.count <= 1 << 20,
          let value = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let statePath = value["state_dir"] as? String, statePath.hasPrefix("/"),
          privateOwnedItem(URL(fileURLWithPath: statePath), kind: mode_t(S_IFDIR)),
          let listen = value["listen"] as? String,
          let origin = URL(string: "http://\(listen)"),
          origin.host == "127.0.0.1", let port = origin.port, (1...65535).contains(port),
          origin.user == nil, origin.password == nil, origin.query == nil, origin.fragment == nil,
          origin.path.isEmpty || origin.path == "/" else { return nil }
    return origin
}

private func privateOwnedItem(_ url: URL, kind: mode_t) -> Bool {
    var info = stat()
    return url.path.withCString { path in
        lstat(path, &info) == 0 && info.st_uid == getuid()
            && info.st_mode & mode_t(S_IFMT) == kind && info.st_mode & 0o077 == 0
    }
}
