import java.security.MessageDigest
import java.util.Properties
import org.gradle.api.attributes.Attribute

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

// 版本号的唯一来源是 ../version.properties（Stage 7 发布门禁）：
// scripts/check-mobile-release.sh 会读同一个文件去核对 APK，两边不一致就失败。
// 这里**不提供默认值**——文件缺失或字段缺失就让构建直接失败，而不是悄悄用 1 和 "0.1.0"。
val appVersion: Properties = Properties().apply {
    val file = rootProject.file("version.properties")
    check(file.isFile) { "missing version.properties: $file" }
    file.inputStream().use { load(it) }
}
val appVersionCode: Int = (appVersion.getProperty("versionCode") ?: error("version.properties 缺少 versionCode"))
    .trim().toIntOrNull() ?: error("version.properties 的 versionCode 不是整数")
val appVersionName: String = (appVersion.getProperty("versionName") ?: error("version.properties 缺少 versionName")).trim()
check(appVersionName.isNotEmpty()) { "version.properties 的 versionName 不能为空" }

android {
    namespace = "com.muyuchat.aether"
    compileSdk = libs.versions.compileSdk.get().toInt()
    // Pinned to the same NDK as scripts/build-android-mobilebridge.sh so AGP
    // strips symbols with the installed toolchain instead of downloading one.
    ndkVersion = "29.0.13113456"

    defaultConfig {
        applicationId = "com.muyuchat.aether"
        minSdk = libs.versions.minSdk.get().toInt()
        targetSdk = libs.versions.targetSdk.get().toInt()
        versionCode = appVersionCode
        versionName = appVersionName
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        ndk {
            // arm64-v8a is the only ABI shipped to real devices. x86_64 is added
            // only when -PaetherAbis=x86_64 (CI emulator gate); see nativeLibDirs.
            abiFilters += (project.findProperty("aetherAbis") as String?)
                ?.split(',')?.map { it.trim() }?.filter { it.isNotEmpty() }
                ?: listOf("arm64-v8a")
        }
    }

    buildFeatures {
        compose = true
    }

    // Stage 7 冒烟签名：**默认关闭**。
    //
    // release 变体默认是没有签名配置的（产出 app-release-unsigned.apk），这是有意的：
    // 发布签名不该由构建脚本自己决定。但"未签名"也意味着 release 变体从没被装上验证过。
    // 所以给一条显式开关：只有 -PaetherSmokeSigning=true 时，才把 **debug keystore**
    // 挂到 release 上，用于 scripts/check-mobile-release-smoke.sh 的装机冒烟。
    // 带这个开关产出的 APK 不可发布、不可分发，脚本会打印 SMOKE SIGNING 警告。
    buildTypes {
        release {
            if ((project.findProperty("aetherSmokeSigning") as String?)?.toBoolean() == true) {
                signingConfig = signingConfigs.getByName("debug")
                matchingFallbacks += listOf("debug")
            }
        }
    }

    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("generated/jniLibs").get().asFile)

    // Go 的 c-shared 库约 33 MB。默认打包方式（Android Gradle Plugin 8+）会把它
    // 按未压缩塞进 APK，实测让包体从 ~46 MB 涨到 ~70 MB。这里改回传统打包
    // （安装时解压），APK 体消掉那 24 MB 冗余；代价是安装时多占一次解压时间。
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

// The Go c-shared library and its JNI shim are build outputs (dist/ is
// gitignored). Sync them into a generated jniLibs source set so a clean build
// still packages exactly the libraries produced by the pinned NDK.
//
// ABIs come from -PaetherAbis (comma separated). Default arm64-v8a keeps the
// device build unchanged; the CI emulator gate passes x86_64.
val targetAbis: List<String> =
    (project.findProperty("aetherAbis") as String?)
        ?.split(',')?.map { it.trim() }?.filter { it.isNotEmpty() }
        ?.takeIf { it.isNotEmpty() }
        ?: listOf("arm64-v8a")

val syncNativeLibs = tasks.register<Sync>("syncNativeLibs") {
    targetAbis.forEach { abi ->
        val dir = rootProject.file("../../dist/android/$abi")
        from(dir) {
            include("*.so")
            into(abi)
        }
    }
    into(layout.buildDirectory.dir("generated/jniLibs"))
    doFirst {
        targetAbis.forEach { abi ->
            val dir = rootProject.file("../../dist/android/$abi")
            val present = dir.listFiles { file -> file.isFile && file.name.endsWith(".so") }
                ?.map { it.name }
                ?.toSet()
                .orEmpty()
            val missing = listOf("libaether_mobilebridge.so", "libaether_jni.so").filterNot(present::contains)
            if (missing.isNotEmpty()) {
                throw GradleException(
                    "missing native libraries for $abi: ${missing.joinToString()}; " +
                        "run scripts/build-android-mobilebridge.sh $abi first",
                )
            }
        }
    }
}
tasks.named("preBuild").configure { dependsOn(syncNativeLibs) }

// 守卫：Kotlin 改动随 APK 走，Go 改动必须重编 dist 里的 .so。曾经出现过
// 「Kotlin 是新代码、Go 二进制落后近一天」的错配（自定义渠道在真机上 404），
// 排查成本很高。这里在构建前比一次时间戳：有任何 internal/**/*.go 比目标
// ABI 的 .so 新，就直接失败并提示去重编。
// 确实只想编 Kotlin 时可用 -PaetherSkipNativeFreshness=true 显式跳过。
val verifyNativeFreshness = tasks.register("verifyNativeFreshness") {
    group = "verification"
    description = "Fails when dist/ native libraries are older than the Go sources they wrap."
    doLast {
        if ((project.findProperty("aetherSkipNativeFreshness") as String?)?.toBoolean() == true) {
            logger.lifecycle("verifyNativeFreshness: skipped by -PaetherSkipNativeFreshness")
            return@doLast
        }
        val goRoot = rootProject.file("../..").toPath().resolve("internal").toFile()
        if (!goRoot.isDirectory) {
            return@doLast
        }
        val newestGo = goRoot.walkTopDown()
            // *_test.go 不进 c-shared 产物，不该触发重编（否则改一条测试就要求重编 33MB 的 .so）。
            .filter { it.isFile && it.name.endsWith(".go") && !it.name.endsWith("_test.go") }
            .maxByOrNull { it.lastModified() }
            ?: return@doLast
        targetAbis.forEach { abi ->
            val so = rootProject.file("../../dist/android/$abi/libaether_mobilebridge.so")
            if (!so.isFile) {
                return@forEach
            }
            if (so.lastModified() < newestGo.lastModified()) {
                throw GradleException(
                    "dist/android/$abi/libaether_mobilebridge.so 比 Go 源码旧" +
                        "（so=${so.lastModified()}，$newestGo=${newestGo.lastModified()}）。" +
                        "请先重编原生库，否则 APK 里的网关缺少最新后端：" +
                        "scripts/build-android-mobilebridge.sh $abi " +
                        "（仅编 Kotlin 时可加 -PaetherSkipNativeFreshness=true）",
                )
            }
        }
    }
}
tasks.named("preBuild").configure { dependsOn(verifyNativeFreshness) }

// Stage 0 acceptance says "CI clean runner resolves the same AAR". Comparing the
// version string and loading a class proves *an* artifact resolved, not that the
// bytes are the vendored ones. This task asks Gradle's own resolver for the
// designsystem artifact file and checks its SHA-256 against
// vendor/mca-designsystem.lock, so a repository-order change or a silently
// different artifact fails the gate.
tasks.register("verifyPinnedDesignSystem") {
    group = "verification"
    description = "Assert the resolved MCA AAR bytes match vendor/mca-designsystem.lock."
    dependsOn("preBuild")
    doLast {
        val lock = rootProject.file("vendor/mca-designsystem.lock")
        check(lock.isFile) { "missing lock file: $lock" }
        val props = Properties()
        lock.inputStream().use { props.load(it) }
        val coordinate = props.getProperty("coordinate")
            ?: throw GradleException("lock has no coordinate")
        val wantAar = props.getProperty("aarSha256")
            ?: throw GradleException("lock has no aarSha256")

        val resolved = configurations.getByName("debugRuntimeClasspath").incoming
            .artifactView {
                attributes { attribute(Attribute.of("artifactType", String::class.java), "aar") }
            }
            .artifacts
            .artifacts
            .filter { artifact -> artifact.id.componentIdentifier.displayName.contains(coordinate) }

        check(resolved.isNotEmpty()) {
            "designsystem artifact not resolved from $coordinate; " +
                "check the maven { url = file(\"vendor/mca-maven\") } entry in settings.gradle.kts"
        }
        resolved.forEach { artifact ->
            val file = artifact.file
            val md = MessageDigest.getInstance("SHA-256")
            val digest = md.digest(file.readBytes()).joinToString("") { byte -> "%02x".format(byte) }
            check(digest == wantAar) {
                "resolved AAR does not match the pin:\n" +
                    "  file = $file\n" +
                    "  want = $wantAar\n" +
                    "  got  = $digest\n" +
                    "The bytes on the classpath are not the vendored artifact."
            }
            logger.lifecycle("pinned designsystem verified: $coordinate sha256=$digest file=$file")
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.material3)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    // Pinned MCA design system; version and checksums live in vendor/mca-designsystem.lock.
    implementation(libs.mca.designsystem)
    testImplementation(libs.junit)
    testImplementation(libs.org.json)
    androidTestImplementation(libs.androidx.test.junit)
    androidTestImplementation(libs.androidx.test.runner)
}

