// tuon-keyhelper keeps tuon's master key behind the Mac's Secure Enclave.
// The private key never leaves the chip; using it needs Touch ID or the Mac
// password. tuon talks to this helper over stdin/stdout.
//
//   tuon-keyhelper create          -> {"key": <wrapped SE key>, "pub": <public key>}
//   tuon-keyhelper derive          stdin {"key","peer","reason"} -> {"secret": ...} (asks Touch ID)
//   tuon-keyhelper check <reason>  -> exit 0 when the owner confirms (Touch ID or password)
import CryptoKit
import Foundation
import LocalAuthentication

func fail(_ msg: String, _ code: Int32 = 1) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(code)
}

func out(_ obj: [String: String]) {
    let data = try! JSONSerialization.data(withJSONObject: obj)
    FileHandle.standardOutput.write(data)
}

let args = CommandLine.arguments
guard args.count >= 2 else { fail("usage: tuon-keyhelper create|derive|check") }
guard SecureEnclave.isAvailable else { fail("no Secure Enclave on this Mac", 3) }

switch args[1] {
case "create":
    var err: Unmanaged<CFError>?
    guard let ac = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
                                                   [.privateKeyUsage, .userPresence], &err) else {
        fail("access control: \(String(describing: err))")
    }
    do {
        let key = try SecureEnclave.P256.KeyAgreement.PrivateKey(accessControl: ac)
        out(["key": key.dataRepresentation.base64EncodedString(),
             "pub": key.publicKey.x963Representation.base64EncodedString()])
    } catch {
        fail("create: \(error)")
    }

case "derive":
    let input = FileHandle.standardInput.readDataToEndOfFile()
    guard let obj = try? JSONSerialization.jsonObject(with: input) as? [String: String],
          let keyB64 = obj["key"], let keyData = Data(base64Encoded: keyB64),
          let peerB64 = obj["peer"], let peerData = Data(base64Encoded: peerB64) else {
        fail("derive: bad input")
    }
    let ctx = LAContext()
    ctx.localizedReason = obj["reason"] ?? "unlock tuon"
    do {
        let key = try SecureEnclave.P256.KeyAgreement.PrivateKey(dataRepresentation: keyData, authenticationContext: ctx)
        let peer = try P256.KeyAgreement.PublicKey(x963Representation: peerData)
        let shared = try key.sharedSecretFromKeyAgreement(with: peer)
        let raw = shared.withUnsafeBytes { Data($0) }
        out(["secret": raw.base64EncodedString()])
    } catch {
        fail("derive: \(error)", 2) // 2: not confirmed or cancelled
    }

case "check":
    let ctx = LAContext()
    let reason = args.count > 2 ? args[2] : "unlock tuon"
    var ok = false
    let done = DispatchSemaphore(value: 0)
    ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason) { success, _ in
        ok = success
        done.signal()
    }
    done.wait()
    exit(ok ? 0 : 2)

default:
    fail("unknown command \(args[1])")
}
