import AVFoundation
import Foundation
import UIKit

/// Отклик на действия. На iPad основной канал — звук и анимация: Taptic Engine
/// здесь нет, поэтому вибро подключено как необязательный бонус, а не как
/// единственный сигнал.
@MainActor
final class Feedback {
    static let shared = Feedback()

    enum Cue: String, CaseIterable {
        case added      // продукт лёг в холодильник
        case removed    // остаток списан
        case cooked     // блюдо готово
        case warning    // не хватает ингредиентов

        var notes: [ToneFactory.Note] {
            switch self {
            case .added:
                [.init(frequency: 987.77, duration: 0.11),
                 .init(frequency: 1_318.51, duration: 0.20, delay: 0.05)]
            case .removed:
                [.init(frequency: 659.25, duration: 0.10),
                 .init(frequency: 493.88, duration: 0.18, delay: 0.04)]
            case .cooked:
                [.init(frequency: 523.25, duration: 0.12),
                 .init(frequency: 659.25, duration: 0.12, delay: 0.05),
                 .init(frequency: 783.99, duration: 0.14, delay: 0.05),
                 .init(frequency: 1_046.50, duration: 0.34, delay: 0.05)]
            case .warning:
                [.init(frequency: 415.30, duration: 0.13),
                 .init(frequency: 349.23, duration: 0.22, delay: 0.03)]
            }
        }
    }

    static let soundKey = "settings.sound.enabled"
    static let hapticsKey = "settings.haptics.enabled"

    private var players: [Cue: AVAudioPlayer] = [:]
    private var sessionReady = false

    private init() {
        UserDefaults.standard.register(defaults: [
            Self.soundKey: true,
            Self.hapticsKey: true,
        ])
    }

    var isSoundEnabled: Bool { UserDefaults.standard.bool(forKey: Self.soundKey) }
    var isHapticsEnabled: Bool { UserDefaults.standard.bool(forKey: Self.hapticsKey) }

    func play(_ cue: Cue) {
        if isSoundEnabled { playSound(cue) }
        if isHapticsEnabled { vibrate(cue) }
    }

    /// Прогрев на старте: первая генерация WAV занимает миллисекунды, и делать
    /// её в момент нажатия — значит услышать звук с задержкой.
    func warmUp() {
        for cue in Cue.allCases { _ = player(for: cue) }
    }

    private func playSound(_ cue: Cue) {
        activateSession()
        guard let player = player(for: cue) else { return }
        player.currentTime = 0
        player.play()
    }

    private func player(for cue: Cue) -> AVAudioPlayer? {
        if let existing = players[cue] { return existing }
        guard let player = try? AVAudioPlayer(data: ToneFactory.wav(cue.notes)) else {
            return nil
        }
        player.prepareToPlay()
        players[cue] = player
        return player
    }

    /// `.ambient` — приложение не глушит чужую музыку и молчит при выключенном звонке.
    private func activateSession() {
        guard !sessionReady else { return }
        sessionReady = true
        let session = AVAudioSession.sharedInstance()
        try? session.setCategory(.ambient, mode: .default, options: [.mixWithOthers])
        try? session.setActive(true)
    }

    private func vibrate(_ cue: Cue) {
        switch cue {
        case .added, .removed:
            UIImpactFeedbackGenerator(style: .light).impactOccurred()
        case .cooked:
            UINotificationFeedbackGenerator().notificationOccurred(.success)
        case .warning:
            UINotificationFeedbackGenerator().notificationOccurred(.warning)
        }
    }
}
