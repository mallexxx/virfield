import Foundation
import XcodesLoginKit

private struct Request: Decodable {
    let op: String
    let account: String?
    let password: String?
    let code: String?
    let phoneID: Int?
    let callback: String?
}

private struct Phone: Encodable {
    let id: Int
    let label: String
}

private struct Reply: Encodable {
    let state: String
    var message: String? = nil
    var idpURL: String? = nil
    var phones: [Phone]? = nil
    var cookies: String? = nil
}

private func send(_ reply: Reply) {
    guard let bytes = try? JSONEncoder().encode(reply), let line = String(data: bytes, encoding: .utf8) else {
        return
    }
    print(line)
    fflush(stdout)
}

private func safeCookie(_ value: String) -> Bool {
    !value.contains { $0 == "\n" || $0 == "\r" || $0 == "\t" }
}

private func downloadCookies(from storage: HTTPCookieStorage?) -> String {
    let rows = (storage?.cookies ?? []).compactMap { cookie -> String? in
        let domain = cookie.domain.lowercased()
        let host = domain.hasPrefix(".") ? String(domain.dropFirst()) : domain
        guard host == "apple.com" || host == "developer.apple.com" || host == "download.developer.apple.com" else { return nil }
        guard safeCookie(cookie.name), safeCookie(cookie.value), safeCookie(cookie.path) else { return nil }
        let broad = domain.hasPrefix(".") || host == "apple.com" || host == "developer.apple.com"
        let expiry = cookie.expiresDate.map { Int64($0.timeIntervalSince1970) } ?? 0
        return "\(domain)\t\(broad ? "TRUE" : "FALSE")\t\(cookie.path)\t\(cookie.isSecure ? "TRUE" : "FALSE")\t\(expiry)\t\(cookie.name)\t\(cookie.value)"
    }
    return rows.joined(separator: "\n") + "\n"
}

@main
struct Main {
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.httpShouldSetCookies = true
        let session = URLSession(configuration: configuration)
        let client = XcodesLoginKit.Client(urlSession: session)
        var current: AuthenticationState = .unauthenticated
        while let line = readLine() {
            guard line.utf8.count <= 8192, let data = line.data(using: .utf8), let request = try? JSONDecoder().decode(Request.self, from: data) else {
                send(Reply(state: "error", message: "Invalid sign-in request"))
                continue
            }
            do {
                switch request.op {
                case "login":
                    guard let account = request.account, account.count <= 256,
                          let password = request.password, password.count <= 1024 else {
                        send(Reply(state: "error", message: "Enter an Apple Account and password"))
                        continue
                    }
                    current = try await client.authenticationState(accountName: account, password: password)
                case "code":
                    guard case .waitingForSecondFactor(let option, _, let sessionData) = current,
                          let code = request.code, code.count <= 12, code.allSatisfy({ $0.isNumber }) else {
                        send(Reply(state: "error", message: "Enter the requested verification code"))
                        continue
                    }
                    switch option {
                    case .codeSent:
                        current = try await client.submitSecurityCode(.device(code: code), sessionData: sessionData)
                    case .smsSent(let phone):
                        current = try await client.submitSecurityCode(.sms(code: code, phoneNumberId: phone.id), sessionData: sessionData)
                    default:
                        send(Reply(state: "error", message: "Choose a verification method first"))
                        continue
                    }
                case "sms":
                    guard case .waitingForSecondFactor(_, let options, let sessionData) = current,
                          let phoneID = request.phoneID,
                          let phone = options.trustedPhoneNumbers?.first(where: { $0.id == phoneID }) else {
                        send(Reply(state: "error", message: "Choose a trusted phone number"))
                        continue
                    }
                    current = try await client.requestSMSSecurityCode(to: phone, authOptions: options, sessionData: sessionData)
                case "federated":
                    guard case .waitingForFederatedAuthentication = current,
                          let callback = request.callback, callback.count <= 4096 else {
                        send(Reply(state: "error", message: "Enter the identity-provider callback URL"))
                        continue
                    }
                    current = try await client.validateFederatedCallbackURLString(callback)
                default:
                    send(Reply(state: "error", message: "Unsupported sign-in request"))
                    continue
                }
                switch current {
                case .authenticated:
                    let cookies = downloadCookies(from: session.configuration.httpCookieStorage)
                    if cookies == "\n" {
                        send(Reply(state: "error", message: "Apple did not provide download session cookies"))
                    } else {
                        send(Reply(state: "authenticated", cookies: cookies))
                    }
                case .waitingForSecondFactor(let option, let options, _):
                    switch option {
                    case .codeSent:
                        send(Reply(state: "code", message: "Enter the code shown on your trusted Apple device"))
                    case .smsSent(let phone):
                        send(Reply(state: "code", message: "Enter the SMS code sent to \(phone.numberWithDialCode)"))
                    case .smsPendingChoice:
                        let phones = (options.trustedPhoneNumbers ?? []).map { Phone(id: $0.id, label: $0.numberWithDialCode) }
                        send(Reply(state: "sms", message: "Choose a trusted phone number", phones: phones))
                    case .securityKey:
                        send(Reply(state: "error", message: "This account requires a security key; this sign-in page cannot complete that challenge"))
                    }
                case .waitingForFederatedAuthentication(let federation):
                    guard let idpURL = federation.idpURL, idpURL.scheme == "https" else {
                        send(Reply(state: "error", message: "Apple did not provide a valid identity-provider URL"))
                        continue
                    }
                    send(Reply(state: "federated", message: "Complete your organization's sign-in, then paste the callback URL", idpURL: idpURL.absoluteString))
                case .notAppleDeveloper:
                    send(Reply(state: "error", message: "This Apple Account cannot access Developer Downloads"))
                case .unauthenticated:
                    send(Reply(state: "error", message: "Apple sign-in did not complete"))
                }
            } catch {
                switch current {
                case .waitingForSecondFactor(let option, let options, _):
                    if case .smsPendingChoice = option {
                        let phones = (options.trustedPhoneNumbers ?? []).map { Phone(id: $0.id, label: $0.numberWithDialCode) }
                        send(Reply(state: "sms", message: "SMS request failed; choose a trusted phone again", phones: phones))
                    } else {
                        send(Reply(state: "code", message: "Verification failed; check the code and try again"))
                    }
                case .waitingForFederatedAuthentication(let federation):
                    send(Reply(state: "federated", message: "The callback was rejected; complete your organization's sign-in again", idpURL: federation.idpURL?.absoluteString))
                default:
                    send(Reply(state: "error", message: "Apple sign-in failed; check the account or Apple service"))
                }
            }
        }
    }
}
