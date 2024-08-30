#!/usr/bin/env bash

declare -A replacements

replacements=(
    ["group: helm.toolkit.fluxcd.io"]="group: cd.qdrant.io"
    ["source.toolkit.fluxcd.io"]="cd.qdrant.io"
    ["finalizers.fluxcd.io"]="finalizers.qdrant.io"
    ["domain: toolkit.fluxcd.io"]="domain: qdrant.io"
    ["group: helm$"]="group: cd"
    ["helm.toolkit.fluxcd.io"]="cd.qdrant.io"
    ["shortName=hr"]="shortName=qdranthr"
)

replace_files_content() {
    for pattern in "${!replacements[@]}"; do
        echo "Renaming '$pattern' to '${replacements[$pattern]}'"
        find . -type f \( -name '*.go' -o -name '*.yml' -o -name '*.yaml' -o -path './docs/*.md'  -o -name 'PROJECT' \) | xargs sed -i '' "s/$pattern/${replacements[$pattern]}/g"
    done

    # special ones (multiline)
    find . -type f \( -name '*.go' -o -name '*.yml' -o -name '*.yaml' -o -path './docs/*.md' -o -name 'PROJECT' \) | xargs sed -i '' '/shortNames:/,/^ *-/s/- hr/- qdranthr/'
}

copy_crd_base_files() {
    old_pattern="helm.toolkit.fluxcd.io"
    new_pattern="cd.qdrant.io"
    for file in config/crd/bases/*.yaml; do
        new_filename=`echo $file | sed s/$old_pattern/$new_pattern/g`
        echo "Copying '$file' to '$new_filename'"
        cp $file $new_filename
    done
}

# For some reason in the original patch we were keeping the original files. We
# do the same here. We use git restore to undo the changes made by the call to
# `replace_files_content`.
restore_original_crd_base_files() {
    git restore config/crd/bases/helm*
}

replace_files_content
copy_crd_base_files
restore_original_crd_base_files
