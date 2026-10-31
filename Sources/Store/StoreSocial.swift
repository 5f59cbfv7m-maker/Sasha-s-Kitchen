import AuthenticationServices
import SwiftUI

// MARK: Аккаунт

/// Вход, выход, удаление аккаунта и список заблокированных.
struct StoreAccountView: View {
    @Environment(\.dismiss) private var dismiss
    private var account: StoreAccount { .shared }
    @State private var error: String?
    @State private var confirmingDelete = false
    @State private var blocked: [BlockedAuthor] = []
    #if DEBUG
    @State private var handle = ""
    @State private var password = ""
    #endif

    struct BlockedAuthor: Decodable, Identifiable {
        let id: String
        let handle: String
    }

    var body: some View {
        NavigationStack {
            Form {
                if let user = account.user {
                    Section {
                        LabeledContent(user.displayName, value: "@" + user.handle)
                        Button("Выйти") { Task { await account.signOut() } }
                    }
                    Section("Заблокированные") {
                        if blocked.isEmpty { Text("Никого").foregroundStyle(.secondary) }
                        ForEach(blocked) { author in
                            HStack {
                                Text(verbatim: "@" + author.handle)
                                Spacer()
                                Button("Разблокировать") {
                                    Task {
                                        try? await StoreAPI.perform("DELETE", "v1/blocks/\(author.id)")
                                        await loadBlocked()
                                    }
                                }
                            }
                        }
                    }
                    Section {
                        Button("Удалить аккаунт", role: .destructive) { confirmingDelete = true }
                    } footer: {
                        Text("Удалятся профиль, комментарии и лайки в магазине. Ваши холодильник и рецепты останутся.")
                    }
                } else {
                    Section {
                        SignInWithAppleButton(.signIn) { account.prepare($0) } onCompletion: { result in
                            Task {
                                do { try await account.complete(result) } catch { self.error = error.localizedDescription }
                            }
                        }
                        .frame(height: 48)
                    } footer: {
                        Text("Аккаунт нужен, чтобы ставить лайки, писать комментарии и жаловаться. Холодильник и рецепты работают без него.")
                    }
                    #if DEBUG
                    Section("Для проверки") {
                        TextField(String("handle"), text: $handle).textInputAutocapitalization(.never)
                        SecureField(String("password"), text: $password)
                        Button(String("Войти или создать")) {
                            Task {
                                do { try await account.debugSignIn(handle: handle, password: password) }
                                catch { self.error = error.localizedDescription }
                            }
                        }
                    }
                    #endif
                }
                if let error {
                    Text(error).foregroundStyle(.orange)
                }
            }
            .navigationTitle("Аккаунт")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Готово") { dismiss() } } }
            .task(id: account.user?.id) { await loadBlocked() }
            .confirmationDialog("Удалить аккаунт?", isPresented: $confirmingDelete, titleVisibility: .visible) {
                Button("Удалить", role: .destructive) {
                    Task {
                        do { try await account.deleteAccount() } catch { self.error = error.localizedDescription }
                    }
                }
            }
        }
    }

    private func loadBlocked() async {
        guard account.user != nil else { blocked = []; return }
        struct Blocks: Decodable { let items: [BlockedAuthor] }
        let list: Blocks? = try? await StoreAPI.get("v1/blocks")
        blocked = list?.items ?? []
    }
}

// MARK: Жалоба и блокировка

/// Меню «…» у чужой публикации: пожаловаться и заблокировать автора
/// (Guideline 1.2 требует и то и другое прямо в интерфейсе).
struct StoreModerationMenu: View {
    let targetType: String
    let targetID: String
    let author: StoreAuthor
    /// После блокировки автор должен исчезнуть с экрана.
    var onBlocked: () -> Void = {}

    @State private var reporting = false
    @State private var needsAccount = false
    @State private var message: String?

    private static let reasons: [(String, LocalizedStringKey)] = [
        ("spam", "Спам"), ("offensive", "Оскорбления"), ("unsafe_food", "Опасно для здоровья"),
        ("copyright", "Чужой контент"), ("sexual", "Непристойное"), ("other", "Другое"),
    ]

    var body: some View {
        Menu {
            Button { act { reporting = true } } label: { Label("Пожаловаться", systemImage: "flag") }
            if let id = author.id, id != StoreAccount.shared.user?.id {
                Button(role: .destructive) {
                    act {
                        Task {
                            do {
                                try await StoreAPI.perform("PUT", "v1/blocks/\(id)")
                                onBlocked()
                            } catch { message = error.localizedDescription }
                        }
                    }
                } label: {
                    Label("Заблокировать @\(author.handle)", systemImage: "hand.raised")
                }
            }
        } label: {
            Image(systemName: "ellipsis.circle").font(.title2).padding(6).contentShape(.rect)
        }
        .accessibilityLabel(Text("Ещё"))
        .confirmationDialog("Пожаловаться", isPresented: $reporting, titleVisibility: .visible) {
            ForEach(Self.reasons, id: \.0) { reason, title in
                Button(title) { Task { await report(reason) } }
            }
        }
        .sheet(isPresented: $needsAccount) { StoreAccountView() }
        .alert(message ?? "", isPresented: Binding { message != nil } set: { if !$0 { message = nil } }) {
            Button("Понятно") {}
        }
    }

    /// Жалобы и блокировки — от имени человека: без входа сначала вход.
    private func act(_ action: () -> Void) {
        if StoreAccount.shared.user == nil { needsAccount = true } else { action() }
    }

    private func report(_ reason: String) async {
        do {
            try await StoreAPI.perform("POST", "v1/reports",
                                       body: ["target_type": targetType, "target_id": targetID, "reason": reason])
            message = String(localized: "Спасибо, жалоба отправлена модераторам")
        } catch {
            message = error.localizedDescription
        }
    }
}

// MARK: Комментарии

struct StoreComment: Decodable, Identifiable, Hashable {
    let id: String
    let body: String
    let author: StoreAuthor
    let replies: [StoreComment]?
    let mine: Bool?
}

/// Обсуждение под рецептом или роликом: kind — "recipes" или "posts".
struct StoreCommentsView: View {
    @Environment(\.dismiss) private var dismiss
    let kind: String
    let id: String

    @State private var comments: [StoreComment] = []
    @State private var draft = ""
    @State private var replyTo: StoreComment?
    @State private var needsAccount = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            List {
                if comments.isEmpty {
                    Text("Комментариев пока нет").foregroundStyle(.secondary)
                }
                ForEach(comments) { comment in
                    row(comment)
                    ForEach(comment.replies ?? []) { reply in
                        row(reply).padding(.leading, 24)
                    }
                }
            }
            .safeAreaInset(edge: .bottom) { composer }
            .navigationTitle("Комментарии")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Готово") { dismiss() } } }
            .task { await load() }
            .sheet(isPresented: $needsAccount) { StoreAccountView() }
        }
    }

    private func row(_ comment: StoreComment) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(verbatim: "@" + comment.author.handle).font(.caption.bold())
                Spacer()
                if comment.mine == true {
                    Button("Удалить", role: .destructive) {
                        Task {
                            try? await StoreAPI.perform("DELETE", "v1/comments/\(comment.id)")
                            await load()
                        }
                    }
                    .font(.caption)
                } else {
                    StoreModerationMenu(targetType: "comment", targetID: comment.id, author: comment.author) {
                        Task { await load() }
                    }
                    .font(.caption)
                }
            }
            Text(comment.body)
            // Один уровень ответов: отвечают только на верхний комментарий.
            if comments.contains(comment) {
                Button("Ответить") { replyTo = comment }.font(.caption).buttonStyle(.borderless)
            }
        }
    }

    private var composer: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let replyTo {
                HStack {
                    Text("Ответ @\(replyTo.author.handle)").font(.caption).foregroundStyle(.secondary)
                    Button { self.replyTo = nil } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.borderless)
                }
            }
            if let error { Text(error).font(.caption).foregroundStyle(.orange) }
            HStack {
                TextField("Комментарий", text: $draft, axis: .vertical).lineLimit(1...4)
                Button {
                    Task { await send() }
                } label: {
                    Image(systemName: "paperplane.fill")
                }
                .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding()
        .background(.bar)
    }

    private func load() async {
        let page: StorePage<StoreComment>? = try? await StoreAPI.get("v1/\(kind)/\(id)/comments")
        comments = page?.items ?? []
    }

    private func send() async {
        guard StoreAccount.shared.user != nil else { needsAccount = true; return }
        var body: [String: Any] = ["body": draft.trimmingCharacters(in: .whitespacesAndNewlines)]
        if let replyTo { body["parent_id"] = replyTo.id }
        do {
            try await StoreAPI.perform("POST", "v1/\(kind)/\(id)/comments", body: body)
            draft = ""
            replyTo = nil
            error = nil
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
