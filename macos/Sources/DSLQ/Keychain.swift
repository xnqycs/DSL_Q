import Foundation
import Security

enum DatabaseKey {
    static func load() throws -> Data {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "local.dslq.database",
            kSecAttrAccount as String: "encryption-key"
        ]
        var search = query
        search[kSecReturnData as String] = true
        search[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(search as CFDictionary, &result)
        if status == errSecSuccess, let key = result as? Data, key.count == 32 { return key }
        guard status == errSecItemNotFound else { throw DSLQError.message("无法读取钥匙串中的下载数据库密钥（\(status)）") }
        var key = Data(count: 32)
        let randomStatus = key.withUnsafeMutableBytes { SecRandomCopyBytes(kSecRandomDefault, 32, $0.baseAddress!) }
        guard randomStatus == errSecSuccess else { throw DSLQError.message("无法生成数据库密钥") }
        var add = query
        add[kSecValueData as String] = key
        add[kSecAttrLabel as String] = "DSL Q 下载数据库"
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let added = SecItemAdd(add as CFDictionary, nil)
        guard added == errSecSuccess else { throw DSLQError.message("无法保存数据库密钥（\(added)）") }
        return key
    }
}
