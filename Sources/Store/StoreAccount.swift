import AuthenticationServices
import CryptoKit
import Foundation
import Observation
import Security

/// Аккаунт магазина: вход через Apple, токены в Связке ключей. Холодильник
/// и рецепты от аккаунта не зависят — он нужен только чтобы лайкать,
/// комментировать, жаловаться и блокировать.
@Observable
final class StoreAccount {
    static let shared = StoreAccount()

    struct User: Codable, Equatable {
        let id: String
        let handle: String
        let displayName: String
    }

    private struct Session: Codable {
        var user: User
        var accessToken: String
        var refreshToken: String
    }

    private var session: Session? {
        didSet { Keychain.save(session) }
    }

    private init() {
        session = Keychain.load()
        #if DEBUG
        // `-storeLogin handle:password` — вход для проверки на симуляторе.
        if let pair = UserDefaults.standard.string(forKey: "storeLogin")?.split(separator: ":", maxSplits: 1),
           pair.count == 2 {
            Task { try? await debugSignIn(handle: String(pair[0]), password: String(pair[1])) }
        }
        #endif
    }

    var user: User? { session?.user }
    var accessToken: String? { session?.accessToken }

    // MARK: Вход через Apple

    /// Случайная строка для запроса; Apple кладёт в токен её SHA-256, а сервер
    /// сверяет — так перехваченный токен не войдёт в чужую сессию.
    private(set) var pendingNonce = ""

    func prepare(_ request: ASAuthorizationAppleIDRequest) {
        pendingNonce = UUID().uuidString + UUID().uuidString
        request.requestedScopes = [.fullName]
        request.nonce = Self.sha256(pendingNonce)
    }

    func complete(_ result: Result<ASAuthorization, Error>) async throws {
        guard case .success(let authorization) = result,
              let credential = authorization.credential as? ASAuthorizationAppleIDCredential,
              let token = credential.identityToken.flatMap({ String(data: $0, encoding: .utf8) })
        else {
            if case .failure(let error) = result, (error as? ASAuthorizationError)?.code == .canceled { return }
            throw StoreAPI.Failure(message: String(localized: "Не удалось войти через Apple"))
        }
        let name = credential.fullName.map { PersonNameComponentsFormatter.localizedString(from: $0, style: .default) } ?? ""
        try await start("auth/apple", ["identity_token": token, "nonce": Self.sha256(pendingNonce), "display_name": name])
    }

    #if DEBUG
    /// Вход по паролю — только для проверки с локальным сервером: личная
    /// команда разработчика не умеет Sign in with Apple.
    func debugSignIn(handle: String, password: String) async throws {
        do {
            try await start("auth/login", ["login": handle, "password": password])
        } catch {
            try await start("auth/register", ["handle": handle, "display_name": handle,
                                             "email": handle + "@example.com", "password": password])
        }
    }
    #endif

    private struct SessionResponse: Decodable {
        let user: User
        let accessToken: String
        let refreshToken: String
    }

    private func start(_ path: String, _ body: [String: Any]) async throws {
        let response: SessionResponse = try await StoreAPI.send("POST", path, body: body)
        session = Session(user: response.user, accessToken: response.accessToken, refreshToken: response.refreshToken)
    }

    /// Новый короткий токен по долгому; не вышло — сессия закончилась.
    func refresh() async -> Bool {
        guard let refreshToken = session?.refreshToken else { return false }
        session?.accessToken = ""
        do {
            let response: SessionResponse = try await StoreAPI.send("POST", "auth/refresh",
                                                                   body: ["refresh_token": refreshToken])
            session = Session(user: response.user, accessToken: response.accessToken, refreshToken: response.refreshToken)
            return true
        } catch {
            session = nil
            return false
        }
    }

    func signOut() async {
        if let refreshToken = session?.refreshToken {
            try? await StoreAPI.perform("POST", "auth/logout", body: ["refresh_token": refreshToken])
        }
        session = nil
    }

    /// Guideline 5.1.1(v): аккаунт удаляется из самого приложения.
    func deleteAccount() async throws {
        try await StoreAPI.perform("DELETE", "auth/me")
        session = nil
    }

    private static func sha256(_ text: String) -> String {
        SHA256.hash(data: Data(text.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

/// Сессия — одной записью в Связке ключей, только на этом устройстве.
private enum Keychain {
    private static let query: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: "com.kirillrychkov.FridgeOracle.store",
        kSecAttrAccount as String: "session",
    ]

    static func save<T: Encodable>(_ value: T?) {
        SecItemDelete(query as CFDictionary)
        guard let value, let data = try? JSONEncoder().encode(value) else { return }
        var item = query
        item[kSecValueData as String] = data
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(item as CFDictionary, nil)
    }

    static func load<T: Decodable>() -> T? {
        var item = query
        item[kSecReturnData as String] = true
        var result: AnyObject?
        guard SecItemCopyMatching(item as CFDictionary, &result) == errSecSuccess, let data = result as? Data else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }
}
