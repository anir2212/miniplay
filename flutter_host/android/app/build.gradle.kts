import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.Base64
import java.util.Properties

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// Release signing. The upload keystore and its passwords are never committed.
// They come from MINIPLAY_UPLOAD_* environment variables, or from an untracked
// properties file: MINIPLAY_KEY_PROPERTIES if set, otherwise android/key.properties
// (git-ignored). Keys: storeFile, storePassword, keyAlias, keyPassword.
val keyProperties =
    Properties().apply {
        val override = System.getenv("MINIPLAY_KEY_PROPERTIES")
        val source = if (override.isNullOrBlank()) rootProject.file("key.properties") else file(override)
        if (source.isFile) source.inputStream().use { load(it) }
    }

fun signingValue(
    property: String,
    environment: String,
): String? =
    System.getenv(environment)?.takeIf { it.isNotBlank() }
        ?: keyProperties.getProperty(property)?.takeIf { it.isNotBlank() }

val uploadStoreFile = signingValue("storeFile", "MINIPLAY_UPLOAD_STORE_FILE")
val uploadStorePassword = signingValue("storePassword", "MINIPLAY_UPLOAD_STORE_PASSWORD")
val uploadKeyAlias = signingValue("keyAlias", "MINIPLAY_UPLOAD_KEY_ALIAS")
val uploadKeyPassword = signingValue("keyPassword", "MINIPLAY_UPLOAD_KEY_PASSWORD")
val hasUploadKey = listOf(uploadStoreFile, uploadStorePassword, uploadKeyAlias, uploadKeyPassword).all { it != null }

// A release or localRelease build without an upload key stops here instead of
// producing an unsigned or debug-signed APK.
if (!hasUploadKey && gradle.startParameter.taskNames.any { it.contains("release", ignoreCase = true) }) {
    throw GradleException(
        "Release signing is not configured. Set MINIPLAY_KEY_PROPERTIES to an untracked properties file " +
            "(storeFile, storePassword, keyAlias, keyPassword) or the MINIPLAY_UPLOAD_* environment variables.",
    )
}

// The localRelease build trusts one local development CA. Its certificate
// (never its key) is read from MINIPLAY_LOCAL_CA_CERT or -Pminiplay.localCaCert
// at build time and is not stored in the repository.
abstract class LocalCaResource : DefaultTask() {
    @get:InputFile
    @get:Optional
    abstract val caCert: RegularFileProperty

    @get:OutputDirectory
    abstract val outputDir: DirectoryProperty

    @TaskAction
    fun copy() {
        if (!caCert.isPresent) {
            throw GradleException(
                "localRelease needs the local CA certificate: set MINIPLAY_LOCAL_CA_CERT or -Pminiplay.localCaCert to its PEM file.",
            )
        }
        val source = caCert.get().asFile
        val text = source.readText()
        if (text.contains("PRIVATE KEY")) {
            throw GradleException("$source contains a private key; pass only the CA certificate.")
        }
        val certificate =
            source.inputStream().use {
                CertificateFactory.getInstance("X.509").generateCertificate(it) as X509Certificate
            }
        if (certificate.basicConstraints < 0) {
            throw GradleException("$source is not a CA certificate.")
        }
        val raw = outputDir.get().dir("raw").asFile
        raw.deleteRecursively()
        raw.mkdirs()
        val pem = Base64.getMimeEncoder(64, "\n".toByteArray()).encodeToString(certificate.encoded)
        raw.resolve("miniplay_local_ca.pem").writeText("-----BEGIN CERTIFICATE-----\n$pem\n-----END CERTIFICATE-----\n")
    }
}

val localCaPath =
    (findProperty("miniplay.localCaCert") as String?)?.takeIf { it.isNotBlank() }
        ?: System.getenv("MINIPLAY_LOCAL_CA_CERT")?.takeIf { it.isNotBlank() }

val prepareLocalCa =
    tasks.register<LocalCaResource>("prepareLocalCa") {
        if (localCaPath != null) caCert.set(file(localCaPath))
        outputDir.set(layout.buildDirectory.dir("generated/miniplay_local_ca/res"))
    }

android {
    namespace = "com.example.miniplay_host"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.example.miniplay_host"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    signingConfigs {
        if (hasUploadKey) {
            create("upload") {
                storeFile = file(uploadStoreFile!!)
                storePassword = uploadStorePassword
                keyAlias = uploadKeyAlias
                keyPassword = uploadKeyPassword
            }
        }
    }

    buildTypes {
        release {
            // Production: HTTPS with system CAs only (src/main/res/xml/network_security_config.xml).
            signingConfig = if (hasUploadKey) signingConfigs.getByName("upload") else null
        }
        // Non-production: the release build plus trust in the local CA for
        // 10.0.2.2, 127.0.0.1 and localhost (src/localRelease/res/xml).
        // Separate application ID so it can sit beside the production app.
        create("localRelease") {
            initWith(getByName("release"))
            // R8 stays off here. Flutter's Gradle plugin copies a custom build
            // type's minify setting to the plugin libraries, and minifying the
            // webview_flutter_android library on its own strips the
            // WebViewFlutterPlugin class the app registers, so R8 then fails.
            isMinifyEnabled = false
            isShrinkResources = false
            applicationIdSuffix = ".local"
            versionNameSuffix = "-local"
            isDebuggable = false
            matchingFallbacks += listOf("release")
            signingConfig = if (hasUploadKey) signingConfigs.getByName("upload") else null
        }
    }
}

androidComponents {
    onVariants(selector().withBuildType("localRelease")) { variant ->
        variant.sources.res?.addGeneratedSourceDirectory(prepareLocalCa, LocalCaResource::outputDir)
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
