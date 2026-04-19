// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import type { FC, SVGProps } from "react";
import {
    ArchiveIcon,
    AudioIcon,
    BabelIcon,
    BunIcon,
    CIcon,
    CppIcon,
    CssIcon,
    DatabaseIcon,
    DockerIcon,
    EditorconfigIcon,
    EnvIcon,
    EslintIcon,
    FileIcon,
    FolderIcon,
    FolderOpenIcon,
    FontIcon,
    GitIcon,
    GoIcon,
    GraphqlIcon,
    HtmlIcon,
    ImageIcon,
    JavaIcon,
    JsIcon,
    JsonIcon,
    LicenseIcon,
    LockIcon,
    MdIcon,
    NpmIcon,
    PhpIcon,
    PnpmIcon,
    PostcssIcon,
    PrettierIcon,
    ProtobufIcon,
    PyIcon,
    ReactJsIcon,
    ReactTsIcon,
    ReadmeIcon,
    RubyIcon,
    RustIcon,
    SassIcon,
    ShellIcon,
    SqlIcon,
    TailwindIcon,
    TomlIcon,
    TsIcon,
    VideoIcon,
    ViteIcon,
    WasmIcon,
    WebpackIcon,
    XmlIcon,
    YamlIcon,
    YarnIcon,
} from "./file-icons";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };
export type IconComp = FC<IconProps>;

// Full-name lookup (case-insensitive). Runs before extension lookup.
const NAME_MAP: Record<string, IconComp> = {
    "package.json": NpmIcon,
    "package-lock.json": NpmIcon,
    "yarn.lock": YarnIcon,
    "pnpm-lock.yaml": PnpmIcon,
    "bun.lockb": BunIcon,
    "bun.lock": BunIcon,
    "readme.md": ReadmeIcon,
    "readme": ReadmeIcon,
    "license": LicenseIcon,
    "license.md": LicenseIcon,
    "license.txt": LicenseIcon,
    "dockerfile": DockerIcon,
    "docker-compose.yml": DockerIcon,
    "docker-compose.yaml": DockerIcon,
    ".dockerignore": DockerIcon,
    ".gitignore": GitIcon,
    ".gitattributes": GitIcon,
    ".gitmodules": GitIcon,
    ".gitkeep": GitIcon,
    ".env": EnvIcon,
    ".env.local": EnvIcon,
    ".env.development": EnvIcon,
    ".env.production": EnvIcon,
    ".editorconfig": EditorconfigIcon,
    "vite.config.ts": ViteIcon,
    "vite.config.js": ViteIcon,
    "webpack.config.js": WebpackIcon,
    "babel.config.js": BabelIcon,
    ".babelrc": BabelIcon,
    "tailwind.config.js": TailwindIcon,
    "tailwind.config.ts": TailwindIcon,
    "postcss.config.js": PostcssIcon,
    "postcss.config.cjs": PostcssIcon,
    "eslint.config.js": EslintIcon,
    "eslint.config.cjs": EslintIcon,
    ".eslintrc": EslintIcon,
    ".eslintrc.js": EslintIcon,
    ".eslintrc.cjs": EslintIcon,
    ".eslintrc.json": EslintIcon,
    ".eslintignore": EslintIcon,
    ".prettierrc": PrettierIcon,
    ".prettierrc.json": PrettierIcon,
    ".prettierrc.yml": PrettierIcon,
    ".prettierignore": PrettierIcon,
    "prettier.config.cjs": PrettierIcon,
    "prettier.config.js": PrettierIcon,
    "taskfile.yml": YamlIcon,
    "taskfile.yaml": YamlIcon,
};

// Extension lookup (case-insensitive, no leading dot).
const EXT_MAP: Record<string, IconComp> = {
    ts: TsIcon,
    mts: TsIcon,
    cts: TsIcon,
    "d.ts": TsIcon,
    tsx: ReactTsIcon,
    js: JsIcon,
    mjs: JsIcon,
    cjs: JsIcon,
    jsx: ReactJsIcon,

    md: MdIcon,
    markdown: MdIcon,
    mdx: MdIcon,

    json: JsonIcon,
    jsonc: JsonIcon,
    json5: JsonIcon,

    yaml: YamlIcon,
    yml: YamlIcon,
    toml: TomlIcon,
    xml: XmlIcon,

    html: HtmlIcon,
    htm: HtmlIcon,
    css: CssIcon,
    scss: SassIcon,
    sass: SassIcon,
    less: CssIcon,

    py: PyIcon,
    pyi: PyIcon,
    go: GoIcon,
    rs: RustIcon,
    java: JavaIcon,
    kt: JavaIcon,
    cpp: CppIcon,
    cc: CppIcon,
    cxx: CppIcon,
    hpp: CppIcon,
    c: CIcon,
    h: CIcon,
    rb: RubyIcon,
    php: PhpIcon,

    sh: ShellIcon,
    bash: ShellIcon,
    zsh: ShellIcon,
    fish: ShellIcon,
    ps1: ShellIcon,

    // Images
    png: ImageIcon,
    jpg: ImageIcon,
    jpeg: ImageIcon,
    gif: ImageIcon,
    webp: ImageIcon,
    bmp: ImageIcon,
    ico: ImageIcon,
    tiff: ImageIcon,
    svg: ImageIcon,
    avif: ImageIcon,

    // Video
    mp4: VideoIcon,
    mov: VideoIcon,
    mkv: VideoIcon,
    webm: VideoIcon,
    avi: VideoIcon,

    // Audio
    mp3: AudioIcon,
    wav: AudioIcon,
    flac: AudioIcon,
    ogg: AudioIcon,
    m4a: AudioIcon,

    // Archives
    zip: ArchiveIcon,
    tar: ArchiveIcon,
    gz: ArchiveIcon,
    tgz: ArchiveIcon,
    bz2: ArchiveIcon,
    xz: ArchiveIcon,
    rar: ArchiveIcon,
    "7z": ArchiveIcon,

    // Fonts
    ttf: FontIcon,
    otf: FontIcon,
    woff: FontIcon,
    woff2: FontIcon,
    eot: FontIcon,

    // Lock / keys
    pem: LockIcon,
    key: LockIcon,
    crt: LockIcon,
    cer: LockIcon,
    lock: LockIcon,

    // Database
    sql: SqlIcon,
    db: DatabaseIcon,
    sqlite: DatabaseIcon,
    sqlite3: DatabaseIcon,

    // Misc
    wasm: WasmIcon,
    graphql: GraphqlIcon,
    gql: GraphqlIcon,
    proto: ProtobufIcon,
};

function getExt(lower: string): string | null {
    if (lower.endsWith(".d.ts")) return "d.ts";
    const dot = lower.lastIndexOf(".");
    if (dot <= 0 || dot >= lower.length - 1) return null;
    return lower.slice(dot + 1);
}

export function getFileIcon(name: string, isDir: boolean, isOpen: boolean): IconComp {
    if (isDir) {
        return isOpen ? FolderOpenIcon : FolderIcon;
    }
    const lower = (name ?? "").toLowerCase();
    const byName = NAME_MAP[lower];
    if (byName) return byName;
    const ext = getExt(lower);
    if (ext) {
        const byExt = EXT_MAP[ext];
        if (byExt) return byExt;
    }
    return FileIcon;
}
