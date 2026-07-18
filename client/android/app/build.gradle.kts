import java.io.File
import java.io.FileInputStream
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.cert.CertificateExpiredException
import java.security.cert.CertificateNotYetValidException
import java.security.cert.X509Certificate
import java.util.Date
import java.util.Properties
import javax.naming.ldap.LdapName
import org.gradle.api.Action
import org.gradle.api.GradleException
import org.gradle.api.execution.TaskExecutionGraph

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

val injectedSigningPropertyPrefix = "android.injected.signing."
val injectedSigningPropertyPresent =
    (
        project.properties.keys +
            gradle.startParameter.projectProperties.keys
    ).any { name ->
        name.startsWith(injectedSigningPropertyPrefix, ignoreCase = true)
    }
if (injectedSigningPropertyPresent) {
    throw GradleException(
        "Android release signing validation failed: injected signing properties are forbidden",
    )
}

data class ReleaseSigningMaterial(
    val propertiesFile: File,
    val storeFile: File,
    val storePassword: String,
    val keyAlias: String,
    val keyPassword: String,
    val certificateSha256: String,
    val storeType: String,
)

data class ReleaseSigningResolution(
    val material: ReleaseSigningMaterial? = null,
    val error: String? = null,
)

fun isMnemoNASSourceRoot(directory: File): Boolean =
    File(directory, "go.mod").isFile &&
        File(directory, "Makefile").isFile &&
        File(directory, "client/pubspec.yaml").isFile

val sourceCheckoutRoot =
    generateSequence(rootProject.projectDir.absoluteFile) { directory ->
        directory.parentFile
    }.firstOrNull { directory ->
        File(directory, ".git").exists() || isMnemoNASSourceRoot(directory)
    } ?: rootProject.projectDir.parentFile.absoluteFile

fun isInsideSourceCheckout(candidate: File): Boolean {
    val checkoutPath = sourceCheckoutRoot.toPath().toAbsolutePath().normalize()
    val candidatePath = candidate.toPath().toAbsolutePath().normalize()
    if (candidatePath.startsWith(checkoutPath)) {
        return true
    }
    return try {
        candidate.canonicalFile.toPath().startsWith(sourceCheckoutRoot.canonicalFile.toPath())
    } catch (_: Exception) {
        true
    }
}

fun resolveReleaseSigningMaterial(): ReleaseSigningResolution {
    val propertyPath =
        providers.gradleProperty("mnemonas.android.keyProperties").orNull
    val environmentPath =
        providers.environmentVariable("MNEMONAS_ANDROID_KEY_PROPERTIES").orNull
    if (propertyPath != null && propertyPath.isBlank()) {
        return ReleaseSigningResolution(
            error = "Gradle property mnemonas.android.keyProperties must not be blank",
        )
    }
    if (environmentPath != null && environmentPath.isBlank()) {
        return ReleaseSigningResolution(
            error = "Environment variable MNEMONAS_ANDROID_KEY_PROPERTIES must not be blank",
        )
    }
    if (propertyPath == null && environmentPath == null) {
        return ReleaseSigningResolution(
            error = "an explicit key properties path is required",
        )
    }

    val propertyFile = propertyPath?.let(rootProject::file)?.absoluteFile
    val environmentFile = environmentPath?.let(rootProject::file)?.absoluteFile
    if (propertyFile != null &&
        environmentFile != null &&
        propertyFile.normalize() != environmentFile.normalize()
    ) {
        return ReleaseSigningResolution(
            error =
                "Gradle property and environment variable select different key properties files",
        )
    }
    val propertiesFile =
        propertyFile ?: environmentFile
            ?: return ReleaseSigningResolution(
                error = "an explicit key properties path is required",
            )
    if (!propertiesFile.isFile || !propertiesFile.canRead()) {
        return ReleaseSigningResolution(
            error = "Android release key properties file is missing or unreadable",
        )
    }
    if (isInsideSourceCheckout(propertiesFile)) {
        return ReleaseSigningResolution(
            error = "Android release key properties must be outside the source checkout",
        )
    }

    val properties = Properties()
    try {
        FileInputStream(propertiesFile).use(properties::load)
    } catch (_: Exception) {
        return ReleaseSigningResolution(
            error = "Android release key properties file could not be read",
        )
    }

    fun required(name: String, trim: Boolean = true): String? {
        val raw = properties.getProperty(name) ?: return null
        if (raw.isBlank()) {
            return null
        }
        return if (trim) raw.trim() else raw
    }

    val storePath = required("storeFile")
    val storePassword = required("storePassword", trim = false)
    val keyAlias = required("keyAlias")
    val keyPassword = required("keyPassword", trim = false)
    val certificateSha256 = required("certificateSha256")
    val missing = buildList {
        if (storePath == null) add("storeFile")
        if (storePassword == null) add("storePassword")
        if (keyAlias == null) add("keyAlias")
        if (keyPassword == null) add("keyPassword")
        if (certificateSha256 == null) add("certificateSha256")
    }
    if (missing.isNotEmpty()) {
        return ReleaseSigningResolution(
            error = "Android release key properties contain missing or blank fields: ${missing.joinToString()}",
        )
    }
    if (!Regex("^[0-9A-Fa-f]{64}$").matches(certificateSha256!!)) {
        return ReleaseSigningResolution(
            error = "certificateSha256 must contain exactly 64 hexadecimal characters",
        )
    }

    val rawStoreFile = File(storePath!!)
    val storeFile =
        if (rawStoreFile.isAbsolute) {
            rawStoreFile
        } else {
            File(propertiesFile.parentFile, storePath)
        }.absoluteFile.normalize()
    if (!storeFile.isFile || !storeFile.canRead()) {
        return ReleaseSigningResolution(
            error = "Android release keystore is missing or unreadable",
        )
    }
    if (isInsideSourceCheckout(storeFile)) {
        return ReleaseSigningResolution(
            error = "Android release keystore must be outside the source checkout",
        )
    }

    val configuredStoreType = properties.getProperty("storeType")?.trim()
    if (configuredStoreType != null && configuredStoreType.isEmpty()) {
        return ReleaseSigningResolution(
            error = "storeType must not be blank when it is present",
        )
    }
    val storeType =
        configuredStoreType ?: when (storeFile.extension.lowercase()) {
            "jks", "keystore" -> "JKS"
            "p12", "pfx", "pkcs12" -> "PKCS12"
            else -> KeyStore.getDefaultType()
        }

    return ReleaseSigningResolution(
        material =
            ReleaseSigningMaterial(
                propertiesFile = propertiesFile,
                storeFile = storeFile,
                storePassword = storePassword!!,
                keyAlias = keyAlias!!,
                keyPassword = keyPassword!!,
                certificateSha256 = certificateSha256.lowercase(),
                storeType = storeType,
            ),
    )
}

fun validateReleaseSigningMaterial(material: ReleaseSigningMaterial) {
    if (!material.propertiesFile.isFile || !material.propertiesFile.canRead()) {
        throw GradleException(
            "Android release signing validation failed: key properties file is missing or unreadable",
        )
    }
    if (!material.storeFile.isFile || !material.storeFile.canRead()) {
        throw GradleException(
            "Android release signing validation failed: keystore is missing or unreadable",
        )
    }

    val keyStore =
        try {
            KeyStore.getInstance(material.storeType).also { store ->
                FileInputStream(material.storeFile).use { input ->
                    store.load(input, material.storePassword.toCharArray())
                }
            }
        } catch (_: Exception) {
            throw GradleException(
                "Android release signing validation failed: keystore or store credentials are invalid",
            )
        }
    if (!keyStore.containsAlias(material.keyAlias)) {
        throw GradleException(
            "Android release signing validation failed: configured key alias was not found",
        )
    }
    if (!keyStore.isKeyEntry(material.keyAlias)) {
        throw GradleException(
            "Android release signing validation failed: configured alias is not a key entry",
        )
    }
    val privateKey =
        try {
            keyStore.getKey(material.keyAlias, material.keyPassword.toCharArray())
        } catch (_: Exception) {
            throw GradleException(
                "Android release signing validation failed: key entry or key credentials are invalid",
            )
        }
    if (privateKey !is PrivateKey) {
        throw GradleException(
            "Android release signing validation failed: configured alias does not contain a private key",
        )
    }
    val certificate = keyStore.getCertificate(material.keyAlias) as? X509Certificate
        ?: throw GradleException(
            "Android release signing validation failed: key entry has no X.509 certificate",
        )
    try {
        certificate.checkValidity(Date())
    } catch (_: CertificateExpiredException) {
        throw GradleException(
            "Android release signing validation failed: signing certificate has expired",
        )
    } catch (_: CertificateNotYetValidException) {
        throw GradleException(
            "Android release signing validation failed: signing certificate is not yet valid",
        )
    }

    val commonName =
        try {
            LdapName(certificate.subjectX500Principal.name)
                .rdns
                .firstOrNull { it.type.equals("CN", ignoreCase = true) }
                ?.value
                ?.toString()
        } catch (_: Exception) {
            null
        }
    if (material.keyAlias.equals("androiddebugkey", ignoreCase = true) ||
        commonName.equals("Android Debug", ignoreCase = true)
    ) {
        throw GradleException(
            "Android release signing validation failed: Android Debug certificates are forbidden",
        )
    }

    val fingerprint =
        MessageDigest.getInstance("SHA-256")
            .digest(certificate.encoded)
            .joinToString(separator = "") { byte -> "%02x".format(byte) }
    if (fingerprint != material.certificateSha256) {
        throw GradleException(
            "Android release signing validation failed: certificate SHA-256 fingerprint does not match",
        )
    }
}

val releaseSigningResolution = resolveReleaseSigningMaterial()
val releaseArtifactTaskNames =
    setOf(
        "assembleRelease",
        "bundleRelease",
        "packageRelease",
        "packageReleaseBundle",
        "packageReleaseUniversalApk",
        "signReleaseBundle",
        "signingConfigWriterRelease",
    )
val releaseProjectPath = project.path
gradle.taskGraph.whenReady(
    object : Action<TaskExecutionGraph> {
        override fun execute(taskGraph: TaskExecutionGraph) {
            val releaseArtifactScheduled =
                taskGraph.allTasks.any { task ->
                    task.project.path == releaseProjectPath &&
                        task.name in releaseArtifactTaskNames
                }
            if (releaseArtifactScheduled) {
                val material =
                    releaseSigningResolution.material
                        ?: throw GradleException(
                            "Android release signing validation failed: ${releaseSigningResolution.error}",
                        )
                validateReleaseSigningMaterial(material)
            }
        }
    },
)
val validateReleaseSigning =
    tasks.register("validateReleaseSigning") {
        group = "verification"
        description = "Validates release keystore identity without exposing credentials."
        notCompatibleWithConfigurationCache(
            "Release signing material must be revalidated for every invocation.",
        )
        outputs.upToDateWhen { false }
        doLast {
            val material =
                releaseSigningResolution.material
                    ?: throw GradleException(
                        "Android release signing validation failed: ${releaseSigningResolution.error}",
                    )
            validateReleaseSigningMaterial(material)
            logger.lifecycle("Android release signing policy: valid")
        }
    }

android {
    namespace = "com.mnemonas.app"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        applicationId = "com.mnemonas.app"
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    signingConfigs {
        releaseSigningResolution.material?.let { material ->
            create("release") {
                keyAlias = material.keyAlias
                keyPassword = material.keyPassword
                storeFile = material.storeFile
                storePassword = material.storePassword
                storeType = material.storeType
            }
        }
    }

    buildTypes {
        debug {
            applicationIdSuffix = ".debug"
            versionNameSuffix = "-debug"
        }
        getByName("profile") {
            applicationIdSuffix = ".profile"
            versionNameSuffix = "-profile"
        }
        release {
            if (releaseSigningResolution.material != null) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}

tasks.configureEach {
    if (releaseSigningResolution.material != null) {
        notCompatibleWithConfigurationCache(
            "Signing credentials must never be serialized into the configuration cache.",
        )
    }
    if (name in
        setOf(
            "assembleRelease",
            "bundleRelease",
            "packageRelease",
            "packageReleaseBundle",
            "packageReleaseUniversalApk",
            "signReleaseBundle",
            "signingConfigWriterRelease",
        )
    ) {
        notCompatibleWithConfigurationCache(
            "Release signing material must be revalidated for every invocation.",
        )
        dependsOn(validateReleaseSigning)
    }
}
